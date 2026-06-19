import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { startJob, keychain, KEY_ACCOUNTS } from "../lib/engine";
import { getSettings, getVoices, type Voice } from "../lib/backend";
import { mapBackendSettings } from "../lib/settingsMap";

const LANGUAGES = ["Vietnamese", "English", "Chinese", "Korean", "Japanese", "Thai"];
const PROVIDERS = ["edge-tts", "vbee"];
const LANG_LOCALE: Record<string, string> = {
  Vietnamese: "vi",
  English: "en",
  Chinese: "zh",
  Korean: "ko",
  Japanese: "ja",
  Thai: "th",
};

export default function NewJob() {
  const navigate = useNavigate();
  const [url, setUrl] = useState("");
  const [lang, setLang] = useState("Vietnamese");
  const [provider, setProvider] = useState("edge-tts");
  const [voice, setVoice] = useState("");
  const [voices, setVoices] = useState<Voice[]>([]);
  const [voicesError, setVoicesError] = useState("");
  const [manualMode, setManualMode] = useState(false);
  const [defaults, setDefaults] = useState<Record<string, unknown>>({});
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    getSettings()
      .then((bs) => {
        setDefaults(mapBackendSettings(bs));
        if (bs["default_voice_provider"]) setProvider(String(bs["default_voice_provider"]));
      })
      .catch(() => {});
  }, []);

  // Load voices whenever the provider changes.
  useEffect(() => {
    let cancelled = false;
    setVoicesError("");
    getVoices(provider)
      .then((vs) => {
        if (cancelled) return;
        setVoices(vs);
      })
      .catch((e) => {
        if (cancelled) return;
        setVoices([]);
        setVoicesError(e instanceof Error ? e.message : "Không tải được danh sách giọng đọc");
      });
    return () => {
      cancelled = true;
    };
  }, [provider]);

  // Voices filtered to the target language locale (fallback: show all).
  const prefix = LANG_LOCALE[lang];
  const filtered = prefix ? voices.filter((v) => v.Locale?.toLowerCase().startsWith(prefix)) : voices;
  const shown = filtered.length > 0 ? filtered : voices;

  // Keep a valid voice selected when the list changes.
  useEffect(() => {
    if (shown.length === 0) {
      setVoice("");
    } else if (!shown.some((v) => v.ShortName === voice)) {
      setVoice(shown[0].ShortName);
    }
  }, [shown, voice]);

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
        settings: {
          ...defaults,
          target_language: lang,
          voice_provider: provider,
          voice,
          manual_mode: manualMode,
        },
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
        <Field label="Link video (TikTok / Douyin)">
          <input
            className={inputCls}
            placeholder="https://www.tiktok.com/@user/video/..."
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
        </Field>

        <Field label="Ngôn ngữ đích">
          <select className={inputCls} value={lang} onChange={(e) => setLang(e.target.value)}>
            {LANGUAGES.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="Nhà cung cấp giọng">
            <select className={inputCls} value={provider} onChange={(e) => setProvider(e.target.value)}>
              {PROVIDERS.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Giọng đọc">
            <select
              className={inputCls}
              value={voice}
              onChange={(e) => setVoice(e.target.value)}
              disabled={shown.length === 0}
            >
              {shown.length === 0 && <option value="">(không có giọng)</option>}
              {shown.map((v) => (
                <option key={v.ShortName} value={v.ShortName}>
                  {v.FriendlyName || v.ShortName} {v.Gender ? `· ${v.Gender}` : ""} ({v.Locale})
                </option>
              ))}
            </select>
          </Field>
        </div>
        {voicesError && <div className="text-xs text-amber-600">{voicesError}</div>}

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

const inputCls = "w-full rounded-lg border border-gray-300 px-3 py-2 text-sm";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <label className="block text-sm font-medium">{label}</label>
      {children}
    </div>
  );
}