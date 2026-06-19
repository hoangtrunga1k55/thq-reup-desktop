# OCR sidecar (RapidOCR / ONNX)

Local subtitle-area detection service. Same HTTP contract as the server's
PaddleOCR service, so `engine/ocr/detect.go` uses it unchanged.

## Run (dev)

```bash
python -m venv .venv && source .venv/bin/activate   # Windows: .venv\Scripts\activate
pip install -r requirements.txt
uvicorn app:app --host 127.0.0.1 --port 8000
```

## Package for the desktop app

Bundle into a single executable with PyInstaller so end users don't need Python:

```bash
pip install pyinstaller
pyinstaller --onefile --name ocr-sidecar app.py
```

The Tauri shell launches this binary on `127.0.0.1:8000` before the engine runs
its first OCR step. (Wiring the spawn lives in Phase 2 — see PLAN.md §7.)