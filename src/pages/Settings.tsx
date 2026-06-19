import { useEffect, useState } from "react";
import { getSettings, type BackendSettings } from "../lib/backend";

// Phase 0: pulls the non-secret defaults from the backend and displays them
// read-only. Phase 1 turns these into an editable local settings form (fonts,
// colors, cover, brand, hook, flip/zoom/volume, voice/provider/speed) that the
// engine's render step consumes.
export default function Settings() {
  const [settings, setSettings] = useState<BackendSettings | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    getSettings()
      .then(setSettings)
      .catch((e) => setError(e instanceof Error ? e.message : "Không tải được settings"));
  }, []);

  return (
    <div className="max-w-2xl space-y-6">
      <h1 className="text-2xl font-semibold">Cấu hình</h1>
      <p className="text-sm text-gray-500">
        Mặc định kéo từ tài khoản web. Phase 1 sẽ cho chỉnh trực tiếp trong app.
      </p>
      {error && <div className="text-sm text-red-600">{error}</div>}
      <pre className="overflow-auto rounded-2xl bg-white p-6 text-xs shadow-sm">
        {settings ? JSON.stringify(settings, null, 2) : "Đang tải…"}
      </pre>
    </div>
  );
}