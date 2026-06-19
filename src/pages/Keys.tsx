import { useEffect, useState } from "react";
import { keychain, KEY_ACCOUNTS } from "../lib/engine";
import { getSettings } from "../lib/backend";

type KeyField = { account: string; label: string; hint: string };

const FIELDS: KeyField[] = [
  { account: KEY_ACCOUNTS.openai, label: "OpenAI API Key", hint: "Whisper + GPT-4o" },
  { account: KEY_ACCOUNTS.thq, label: "THQ Solution API Key", hint: "Tải video TikTok/Douyin" },
  { account: KEY_ACCOUNTS.srtVoice, label: "SRT-To-Voice API Key", hint: "Giọng đọc AI (TTS)" },
  { account: KEY_ACCOUNTS.facebook, label: "Facebook Page Token", hint: "Tùy chọn — đăng bài" },
];

export default function Keys() {
  const [values, setValues] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState("");

  useEffect(() => {
    // Pre-fill which keys exist locally (we store secrets in the OS keychain).
    (async () => {
      const next: Record<string, string> = {};
      for (const f of FIELDS) next[f.account] = (await keychain.get(f.account)) ? "••••••••" : "";
      setValues(next);
    })();
    // Optionally surface which keys the backend already has (informational).
    getSettings().catch(() => {});
  }, []);

  async function save(account: string) {
    const v = values[account];
    if (!v || v.startsWith("•")) return; // unchanged
    await keychain.set(account, v);
    setValues((p) => ({ ...p, [account]: "••••••••" }));
    setSaved(account);
    setTimeout(() => setSaved(""), 1500);
  }

  return (
    <div className="max-w-xl space-y-6">
      <h1 className="text-2xl font-semibold">API Keys</h1>
      <p className="text-sm text-gray-500">
        Key được lưu an toàn trong keychain của hệ điều hành, không gửi lên server.
      </p>
      <div className="space-y-4 rounded-2xl bg-white p-6 shadow-sm">
        {FIELDS.map((f) => (
          <div key={f.account} className="space-y-1">
            <label className="block text-sm font-medium">
              {f.label} <span className="text-xs text-gray-400">— {f.hint}</span>
            </label>
            <div className="flex gap-2">
              <input
                type="password"
                className="flex-1 rounded-lg border border-gray-300 px-3 py-2 text-sm"
                value={values[f.account] ?? ""}
                onChange={(e) => setValues((p) => ({ ...p, [f.account]: e.target.value }))}
              />
              <button
                onClick={() => save(f.account)}
                className="rounded-lg bg-indigo-600 px-3 py-2 text-sm font-medium text-white hover:bg-indigo-700"
              >
                {saved === f.account ? "Đã lưu" : "Lưu"}
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}