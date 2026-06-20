import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { startJob } from "../lib/engine";
import { getVoices, type Credentials, type Voice } from "../lib/backend";
import { loadCredentials } from "../lib/credentials";
import { mapBackendSettings } from "../lib/settingsMap";

const LANGUAGES = ["Vietnamese", "English", "Chinese", "Japanese", "Korean", "French", "German", "Spanish", "Thai", "Indonesian"];
const LANGUAGE_TO_LOCALE: Record<string, string> = {
  Vietnamese: "vi", English: "en", Chinese: "zh", Japanese: "ja", Korean: "ko",
  French: "fr", German: "de", Spanish: "es", Thai: "th", Indonesian: "id",
};
const PROVIDERS = ["edge-tts", "vbee"];

const localePrefix = (v: Voice) => (v.Locale || "").split("-")[0].toLowerCase();

export default function NewJob() {
  const navigate = useNavigate();
  const [url, setUrl] = useState("");
  // Flow: provider -> language (derived from provider voices) -> voice.
  const [provider, setProvider] = useState("edge-tts");
  const [allVoices, setAllVoices] = useState<Voice[]>([]);
  const [language, setLanguage] = useState("Vietnamese");
  const [voice, setVoice] = useState("");
  const [voicesLoading, setVoicesLoading] = useState(false);
  const [voicesError, setVoicesError] = useState("");
  const [manualMode, setManualMode] = useState(false);
  const [cred, setCred] = useState<Credentials | null>(null);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const availableLanguages = useMemo(
    () => LANGUAGES.filter((l) => allVoices.some((v) => localePrefix(v) === LANGUAGE_TO_LOCALE[l])),
    [allVoices]
  );
  const voicesForLang = useMemo(
    () => allVoices.filter((v) => localePrefix(v) === LANGUAGE_TO_LOCALE[language]),
    [allVoices, language]
  );

  // Pull keys + settings from the backend (zero-config exe).
  useEffect(() => {
    loadCredentials()
      .then((c) => {
        setCred(c);
        if (c.settings?.default_voice_provider) setProvider(String(c.settings.default_voice_provider));
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Không tải được cấu hình từ auto-reup"));
  }, []);

  // Provider -> load ALL its voices.
  useEffect(() => {
    let cancelled = false;
    setVoicesLoading(true);
    setVoicesError("");
    setAllVoices([]);
    getVoices(provider)
      .then((vs) => !cancelled && setAllVoices(vs))
      .catch((e) => {
        if (cancelled) return;
        setVoicesError(e instanceof Error ? e.message : "Không tải được danh sách giọng");
      })
      .finally(() => !cancelled && setVoicesLoading(false));
    return () => {
      cancelled = true;
    };
  }, [provider]);

  // Keep language valid for the provider.
  useEffect(() => {
    if (availableLanguages.length > 0 && !availableLanguages.includes(language)) {
      setLanguage(availableLanguages[0]);
    }
  }, [availableLanguages, language]);

  // Keep voice valid for the language.
  useEffect(() => {
    if (voicesForLang.length === 0) {
      setVoice("");
    } else if (!voicesForLang.some((v) => v.ShortName === voice)) {
      const preferred = provider === "vbee"
        ? voicesForLang.find((v) => /ngochuyen/i.test(v.ShortName) || /ngọc huyền/i.test(v.FriendlyName))
        : undefined;
      setVoice((preferred ?? voicesForLang[0]).ShortName);
    }
  }, [voicesForLang, voice, provider]);

  async function onStart() {
    setError("");
    setSubmitting(true);
    try {
      const c = cred ?? (await loadCredentials());
      const keys = c.keys;
      if (!keys?.openai || !keys?.thq || !keys?.srt_voice) {
        setError("Tài khoản auto-reup chưa cấu hình đủ API key (OpenAI / THQ / SRT-Voice).");
        setSubmitting(false);
        return;
      }
      const jobId = `${Date.now()}`;
      await startJob(jobId, {
        source_url: url,
        keys,
        settings: {
          ...mapBackendSettings(c.settings),
          target_language: language,
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

        <Field label="Nhà cung cấp giọng">
          <select className={inputCls} value={provider} onChange={(e) => setProvider(e.target.value)}>
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </Field>

        <Field label="Ngôn ngữ đích">
          {voicesLoading ? (
            <div className="text-sm text-gray-400">Đang tải ngôn ngữ…</div>
          ) : availableLanguages.length > 0 ? (
            <select className={inputCls} value={language} onChange={(e) => setLanguage(e.target.value)}>
              {availableLanguages.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
          ) : (
            <div className="text-sm text-amber-600">{voicesError || "Không có ngôn ngữ (kiểm tra SRT-Voice key)."}</div>
          )}
        </Field>

        <Field label="Giọng đọc">
          <select className={inputCls} value={voice} onChange={(e) => setVoice(e.target.value)} disabled={voicesForLang.length === 0}>
            {voicesForLang.length === 0 && <option value="">(không có giọng)</option>}
            {voicesForLang.map((v) => (
              <option key={v.ShortName} value={v.ShortName}>
                {v.FriendlyName || v.ShortName} {v.Gender ? `· ${v.Gender}` : ""} ({v.Locale})
              </option>
            ))}
          </select>
        </Field>

        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={manualMode} onChange={(e) => setManualMode(e.target.checked)} />
          Chế độ thủ công (xác nhận vùng subtitle &amp; nội dung trước khi render)
        </label>

        {error && <div className="text-sm text-red-600">{error}</div>}
        <button
          onClick={onStart}
          disabled={!url || !voice || submitting}
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