import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { startJob, keychain, KEY_ACCOUNTS } from "../lib/engine";
import { getSettings } from "../lib/backend";
import { mapBackendSettings } from "../lib/settingsMap";

const LANGUAGES = ["Vietnamese", "English", "Chinese", "Korean", "Japanese", "Thai"];

export default function NewJob() {
  const navigate = useNavigate();
  const [url, setUrl] = useState("");
  const [lang, setLang] = useState("Vietnamese");
  const [manualMode, setManualMode] = useState(false);
  const [defaults, setDefaults] = useState<Record<string, unknown>>({});
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    // Best-effort: pull non-secret render/voice defaults from the backend.
    getSettings()
      .then((bs) => setDefaults(mapBackendSettings(bs)))
      .catch(() => {});
  }, []);

  async function onStart() {
    setError("");
    setSubmitting(true);
    try {
      const [openai, thq, srtVoice, facebookToken] = await Promise.all([
        keychain.get(KEY_ACCOUNTS.openai),
        keychain.get(KEY_ACCOUNTS.thq),
        keychain.get(KEY_ACCOUNTS.srtVoice),
        keychain.get(KEY_ACCOUNTS.facebook),
      ]);
      if (!openai || !thq || !srtVoice) {
        setError("Thiếu API key (OpenAI / THQ / SRT-Voice). Vào mục API Keys để nhập.");
        setSubmitting(false);
        return;
      }

      const jobId = `${Date.now()}`;
      await startJob(jobId, {
        source_url: url,
        keys: { openai, thq, srt_voice: srtVoice, facebook_token: facebookToken },
        settings: { ...defaults, target_language: lang, manual_mode: manualMode },
      });
      navigate(`/jobs/${jobId}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Không gửi được job tới engine");
      setSubmitting(false);
    }
  }

  return (
    <div className="max-w-xl space-y-6">
      <h1 className="text-2xl font-semibold">Tạo job mới</h1>
      <div className="space-y-4 rounded-2xl bg-white p-6 shadow-sm">
        <div className="space-y-1">
          <label className="block text-sm font-medium">Link video (TikTok / Douyin)</label>
          <input
            className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
            placeholder="https://www.tiktok.com/@user/video/..."
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
        </div>
        <div className="space-y-1">
          <label className="block text-sm font-medium">Ngôn ngữ đích</label>
          <select
            className="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm"
            value={lang}
            onChange={(e) => setLang(e.target.value)}
          >
            {LANGUAGES.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={manualMode} onChange={(e) => setManualMode(e.target.checked)} />
          Chế độ thủ công (xác nhận vùng subtitle &amp; nội dung trước khi render)
        </label>
        {error && <div className="text-sm text-red-600">{error}</div>}
        <button
          onClick={onStart}
          disabled={!url || submitting}
          className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:opacity-50"
        >
          {submitting ? "Đang bắt đầu…" : "Bắt đầu xử lý"}
        </button>
      </div>
    </div>
  );
}