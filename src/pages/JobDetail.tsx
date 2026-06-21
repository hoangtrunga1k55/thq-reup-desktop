import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { convertFileSrc } from "@tauri-apps/api/core";
import { cancelJob, confirmContent, confirmSubtitle, deleteJob, onEngineEvent, type EngineEvent, type Region } from "../lib/engine";
import SubtitleAreaEditor from "../components/SubtitleAreaEditor";
import AIContentEditor, { type AIContent } from "../components/AIContentEditor";

type Phase = "running" | "waiting_subtitle" | "waiting_content" | "completed" | "failed";

type WaitingSub = { video_width: number; video_height: number; preview_frame: string; region: Region };
type WaitingContent = { translated_srt: string; ai_content: AIContent | null; hook_text: string };

export default function JobDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [phase, setPhase] = useState<Phase>("running");
  const [percent, setPercent] = useState(0);
  const [status, setStatus] = useState("Đang chờ engine…");
  const [logs, setLogs] = useState<string[]>([]);
  const [sub, setSub] = useState<WaitingSub | null>(null);
  const [content, setContent] = useState<WaitingContent | null>(null);
  const [outputPath, setOutputPath] = useState("");
  const [aiContent, setAiContent] = useState<AIContent | null>(null);
  const [hookText, setHookText] = useState("");
  const [error, setError] = useState("");
  const bottom = useRef<HTMLDivElement>(null);

  async function onDelete() {
    if (!window.confirm("Xóa job này và toàn bộ dữ liệu cục bộ?")) return;
    try {
      await deleteJob(id);
      navigate("/");
    } catch (e) {
      setError(String(e));
    }
  }

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
          setAiContent(p.ai_content ?? null);
          setHookText(p.hook_text ?? "");
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

  function onCancel() {
    cancelJob(id).catch((e) => setError(String(e)));
    setPhase("failed");
    setStatus("Đã hủy");
  }

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
        <div className="flex gap-2">
          {phase === "running" && (
            <button
              onClick={onCancel}
              className="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100"
            >
              Hủy
            </button>
          )}
          <button
            onClick={onDelete}
            className="rounded-lg border border-red-300 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50"
          >
            Xóa
          </button>
        </div>
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
            onCancel={onCancel}
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
            onCancel={onCancel}
          />
        </div>
      )}

      {phase === "completed" && (
        <div className="space-y-4">
          {outputPath && (
            <div className="space-y-3 rounded-2xl bg-white p-6 shadow-sm">
              <div className="text-sm font-medium text-green-700">Video đã render xong ✅</div>
              <video src={convertFileSrc(outputPath)} controls className="w-full max-w-xs rounded-lg" />
              <div className="break-all text-xs text-gray-400">{outputPath}</div>
            </div>
          )}
          <AIContentResult ai={aiContent} hook={hookText} />
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

// AIContentResult shows the AI-generated metadata with per-field copy buttons,
// so the user can grab the title/description/caption/hashtags for posting.
function AIContentResult({ ai, hook }: { ai: AIContent | null; hook: string }) {
  const [copied, setCopied] = useState("");

  async function copy(text: string, key: string) {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      setTimeout(() => setCopied(""), 1200);
    } catch {
      /* clipboard blocked — user can still select the text */
    }
  }

  const rows: { key: string; label: string; value: string; multiline?: boolean }[] = [
    { key: "title", label: "Tiêu đề", value: ai?.title ?? "" },
    { key: "caption", label: "Caption", value: ai?.caption ?? "", multiline: true },
    { key: "hashtags", label: "Hashtags", value: (ai?.hashtags ?? []).map((h) => `#${h}`).join(" ") },
    { key: "hook", label: "Hook", value: hook },
  ].filter((r) => r.value.trim() !== "");

  if (rows.length === 0) return null;

  return (
    <div className="space-y-3 rounded-2xl bg-white p-6 shadow-sm">
      <div className="text-sm font-medium text-gray-700">Nội dung AI (copy để đăng)</div>
      {rows.map((r) => (
        <div key={r.key} className="space-y-1">
          <div className="flex items-center justify-between">
            <label className="text-xs font-medium text-gray-500">{r.label}</label>
            <button
              onClick={() => copy(r.value, r.key)}
              className="rounded-md border border-gray-300 px-2 py-0.5 text-xs text-gray-600 hover:bg-gray-100"
            >
              {copied === r.key ? "Đã copy" : "Copy"}
            </button>
          </div>
          {r.multiline ? (
            <textarea
              readOnly
              value={r.value}
              rows={2}
              className="w-full rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm"
            />
          ) : (
            <input
              readOnly
              value={r.value}
              className="w-full rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm"
            />
          )}
        </div>
      ))}
    </div>
  );
}
