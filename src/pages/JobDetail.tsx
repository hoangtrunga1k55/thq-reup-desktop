import { useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { convertFileSrc } from "@tauri-apps/api/core";
import { cancelJob, confirmContent, confirmSubtitle, onEngineEvent, type EngineEvent, type Region } from "../lib/engine";
import SubtitleAreaEditor from "../components/SubtitleAreaEditor";
import AIContentEditor, { type AIContent } from "../components/AIContentEditor";

type Phase = "running" | "waiting_subtitle" | "waiting_content" | "completed" | "failed";

type WaitingSub = { video_width: number; video_height: number; preview_frame: string; region: Region };
type WaitingContent = { translated_srt: string; ai_content: AIContent | null; hook_text: string };

export default function JobDetail() {
  const { id = "" } = useParams();
  const [phase, setPhase] = useState<Phase>("running");
  const [percent, setPercent] = useState(0);
  const [status, setStatus] = useState("Đang chờ engine…");
  const [logs, setLogs] = useState<string[]>([]);
  const [sub, setSub] = useState<WaitingSub | null>(null);
  const [content, setContent] = useState<WaitingContent | null>(null);
  const [outputPath, setOutputPath] = useState("");
  const [error, setError] = useState("");
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const off = onEngineEvent(id, (e: EngineEvent) => {
      const p = e.payload ?? {};
      if (p.percent != null) setPercent(p.percent);
      switch (e.type) {
        case "step":
        case "progress":
          setStatus(p.message ?? e.type);
          setPhase("running");
          break;
        case "log":
          setLogs((l) => [...l, `[${p.level}] ${p.message}`]);
          break;
        case "waiting_subtitle":
          setSub(p as WaitingSub);
          setPhase("waiting_subtitle");
          setStatus("Chờ xác nhận vùng subtitle");
          break;
        case "waiting_content":
          setContent(p as WaitingContent);
          setPhase("waiting_content");
          setStatus("Chờ xác nhận nội dung");
          break;
        case "completed":
          setOutputPath(p.output_path ?? "");
          setPercent(100);
          setPhase("completed");
          setStatus("Hoàn tất");
          break;
        case "failed":
        case "error":
          setError(p.error ?? "Lỗi không xác định");
          setPhase("failed");
          break;
      }
    });
    return off;
  }, [id]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs]);

  function onConfirmSub(region: Region) {
    confirmSubtitle(id, region).catch((e) => setError(String(e)));
    setPhase("running");
    setStatus("Đã xác nhận vùng subtitle, tiếp tục…");
  }

  function onConfirmContent(edit: { translated_srt: string; ai_content: AIContent; hook_text: string }) {
    confirmContent(id, edit).catch((e) => setError(String(e)));
    setPhase("running");
    setStatus("Đã xác nhận nội dung, tiếp tục…");
  }

  return (
    <div className="max-w-2xl space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Job #{id}</h1>
        {phase === "running" && (
          <button
            onClick={() => cancelJob(id)}
            className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100"
          >
            Hủy
          </button>
        )}
      </div>

      <div className="space-y-2 rounded-2xl bg-white p-6 shadow-sm">
        <div className="flex justify-between text-sm">
          <span>{status}</span>
          <span>{percent}%</span>
        </div>
        <div className="h-2 w-full overflow-hidden rounded-full bg-gray-100">
          <div
            className={`h-full transition-all ${phase === "failed" ? "bg-red-500" : "bg-indigo-600"}`}
            style={{ width: `${percent}%` }}
          />
        </div>
      </div>

      {error && <div className="rounded-lg bg-red-50 p-3 text-sm text-red-700">{error}</div>}

      {phase === "waiting_subtitle" && sub && (
        <div className="rounded-2xl bg-white p-6 shadow-sm">
          <SubtitleAreaEditor
            frameUrl={convertFileSrc(sub.preview_frame)}
            videoWidth={sub.video_width}
            videoHeight={sub.video_height}
            initial={sub.region}
            onConfirm={onConfirmSub}
          />
        </div>
      )}

      {phase === "waiting_content" && content && (
        <div className="rounded-2xl bg-white p-6 shadow-sm">
          <AIContentEditor
            srt={content.translated_srt}
            content={content.ai_content}
            hookText={content.hook_text}
            onConfirm={onConfirmContent}
          />
        </div>
      )}

      {phase === "completed" && outputPath && (
        <div className="space-y-3 rounded-2xl bg-white p-6 shadow-sm">
          <div className="text-sm font-medium text-green-700">Video đã render xong ✅</div>
          <video src={convertFileSrc(outputPath)} controls className="w-full max-w-xs rounded-lg" />
          <div className="break-all text-xs text-gray-400">{outputPath}</div>
        </div>
      )}

      <div className="rounded-2xl bg-white p-4 shadow-sm">
        <div className="mb-2 text-sm font-medium text-gray-600">Nhật ký</div>
        <div className="max-h-60 space-y-1 overflow-auto font-mono text-xs text-gray-700">
          {logs.length === 0 && <div className="text-gray-400">Chưa có log.</div>}
          {logs.map((l, i) => (
            <div key={i}>{l}</div>
          ))}
          <div ref={bottom} />
        </div>
      </div>
    </div>
  );
}