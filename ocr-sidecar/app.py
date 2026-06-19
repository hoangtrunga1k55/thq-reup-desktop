"""Local OCR sidecar for the desktop app.

Drop-in replacement for the server's PaddleOCR service (ocr/app.py), but built on
RapidOCR (ONNX runtime) so it bundles into a Windows app without PaddlePaddle and
without AVX-512 SIGILL issues. The HTTP contract is identical to the original so
the Go `ocr` package (engine/ocr/detect.go) talks to it unchanged.

  POST /detect   multipart form-data, field "file" (JPG/PNG)
  ->  { "width": int, "height": int,
        "lines": [ { "text": str, "conf": float, "x": int, "y": int, "w": int, "h": int }, ... ] }
"""

import numpy as np
import cv2
from fastapi import FastAPI, UploadFile, File
from rapidocr_onnxruntime import RapidOCR

app = FastAPI()

# RapidOCR ships PP-OCR ONNX models; no language flag needed (multilingual det +
# Chinese/English rec, matching the original lang='ch' behaviour closely enough
# for subtitle-area detection where character count + position drive the choice).
_ocr = RapidOCR()


@app.get("/health")
def health():
    return {"ok": True}


@app.post("/detect")
async def detect(file: UploadFile = File(...)):
    data = await file.read()
    img = cv2.imdecode(np.frombuffer(data, np.uint8), cv2.IMREAD_COLOR)
    if img is None:
        return {"width": 0, "height": 0, "lines": []}

    h, w = img.shape[:2]
    result, _ = _ocr(img)  # result: list of [box(4 pts), text, score] or None

    lines = []
    for item in result or []:
        box, text, score = item[0], item[1], float(item[2])
        xs = [p[0] for p in box]
        ys = [p[1] for p in box]
        x0, y0, x1, y1 = int(min(xs)), int(min(ys)), int(max(xs)), int(max(ys))
        lines.append(
            {
                "text": text,
                "conf": score,
                "x": x0,
                "y": y0,
                "w": x1 - x0,
                "h": y1 - y0,
            }
        )

    return {"width": w, "height": h, "lines": lines}


if __name__ == "__main__":
    # Entry point for the PyInstaller-frozen binary: serve on localhost so the
    # Go engine can reach it at http://127.0.0.1:8000/detect.
    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=8000)