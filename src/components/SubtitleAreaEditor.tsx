import { useEffect, useRef, useState } from "react";
import type { Region } from "../lib/engine";

type Props = {
  frameUrl: string;
  videoWidth: number;
  videoHeight: number;
  initial: Region;
  onConfirm: (region: Region) => void;
  onCancel: () => void;
};

type DragState = {
  mode: "move" | "resize";
  startX: number; // pointer start (client px)
  startY: number;
  startRegion: Region;
};

// Lets the user adjust the cover/subtitle rectangle over the preview frame.
// The rectangle is kept in VIDEO coordinates; we scale to the rendered image.
export default function SubtitleAreaEditor({ frameUrl, videoWidth, videoHeight, initial, onConfirm, onCancel }: Props) {
  const [region, setRegion] = useState<Region>(initial);
  const imgRef = useRef<HTMLImageElement>(null);
  const drag = useRef<DragState | null>(null);
  const [scale, setScale] = useState(1);

  // Recompute the display scale whenever the image lays out or the window resizes.
  useEffect(() => {
    const recompute = () => {
      const el = imgRef.current;
      if (el && videoWidth > 0) setScale(el.getBoundingClientRect().width / videoWidth);
    };
    recompute();
    window.addEventListener("resize", recompute);
    return () => window.removeEventListener("resize", recompute);
  }, [videoWidth]);

  useEffect(() => {
    function onMove(e: PointerEvent) {
      const d = drag.current;
      if (!d || scale === 0) return;
      const dx = (e.clientX - d.startX) / scale;
      const dy = (e.clientY - d.startY) / scale;
      setRegion(() => {
        const r = { ...d.startRegion };
        if (d.mode === "move") {
          r.x = clamp(d.startRegion.x + dx, 0, videoWidth - r.width);
          r.y = clamp(d.startRegion.y + dy, 0, videoHeight - r.height);
        } else {
          r.width = clamp(d.startRegion.width + dx, 20, videoWidth - r.x);
          r.height = clamp(d.startRegion.height + dy, 12, videoHeight - r.y);
        }
        return r;
      });
    }
    function onUp() {
      drag.current = null;
    }
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    return () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
    };
  }, [scale, videoWidth, videoHeight]);

  function begin(mode: "move" | "resize", e: React.PointerEvent) {
    e.preventDefault();
    e.stopPropagation();
    drag.current = { mode, startX: e.clientX, startY: e.clientY, startRegion: region };
  }

  return (
    <div className="space-y-3">
      <div className="text-sm text-gray-600">Kéo khung để chỉnh vùng che subtitle gốc, rồi xác nhận.</div>
      <div className="relative inline-block select-none" style={{ maxWidth: 360 }}>
        <img
          ref={imgRef}
          src={frameUrl}
          alt="preview"
          className="w-full rounded-lg"
          onLoad={() => {
            const el = imgRef.current;
            if (el && videoWidth > 0) setScale(el.getBoundingClientRect().width / videoWidth);
          }}
        />
        <div
          onPointerDown={(e) => begin("move", e)}
          className="absolute cursor-move border-2 border-indigo-500 bg-indigo-500/30"
          style={{
            left: region.x * scale,
            top: region.y * scale,
            width: region.width * scale,
            height: region.height * scale,
          }}
        >
          <div
            onPointerDown={(e) => begin("resize", e)}
            className="absolute -bottom-1.5 -right-1.5 h-3 w-3 cursor-se-resize rounded-sm bg-indigo-600"
          />
        </div>
      </div>
      <div className="text-xs text-gray-400">
        x:{Math.round(region.x)} y:{Math.round(region.y)} w:{Math.round(region.width)} h:{Math.round(region.height)}
      </div>
      <div className="flex gap-2">
        <button
          onClick={() => onConfirm(region)}
          className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
        >
          Xác nhận vùng subtitle
        </button>
        <button
          onClick={onCancel}
          className="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100"
        >
          Hủy
        </button>
      </div>
    </div>
  );
}

function clamp(v: number, lo: number, hi: number): number {
  if (hi < lo) return lo;
  return Math.max(lo, Math.min(hi, v));
}