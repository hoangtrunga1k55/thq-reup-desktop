import { useState } from "react";

export type AIContent = {
  title?: string;
  caption?: string;
  hashtags?: string[];
};

type Props = {
  srt: string;
  content: AIContent | null;
  hookText: string;
  onConfirm: (edit: { translated_srt: string; ai_content: AIContent; hook_text: string }) => void;
  onCancel: () => void;
};

// Lets the user review/edit the translated subtitle + AI content before render.
export default function AIContentEditor({ srt, content, hookText, onConfirm, onCancel }: Props) {
  const [title, setTitle] = useState(content?.title ?? "");
  const [caption, setCaption] = useState(content?.caption ?? "");
  const [hashtags, setHashtags] = useState((content?.hashtags ?? []).join(", "));
  const [hook, setHook] = useState(hookText);
  const [srtText, setSrtText] = useState(srt);

  function confirm() {
    onConfirm({
      translated_srt: srtText,
      hook_text: hook,
      ai_content: {
        ...content,
        title,
        caption,
        hashtags: hashtags
          .split(",")
          .map((h) => h.trim())
          .filter(Boolean),
      },
    });
  }

  return (
    <div className="space-y-4">
      <div className="text-sm text-gray-600">Kiểm tra & chỉnh nội dung trước khi tạo giọng đọc và render.</div>

      <Field label="Tiêu đề">
        <input className={inputCls} value={title} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Field label="Caption">
        <textarea className={inputCls} rows={3} value={caption} onChange={(e) => setCaption(e.target.value)} />
      </Field>
      <Field label="Hashtags (phân cách bởi dấu phẩy)">
        <input className={inputCls} value={hashtags} onChange={(e) => setHashtags(e.target.value)} />
      </Field>
      <Field label="Hook text (hiện đầu video)">
        <input className={inputCls} value={hook} onChange={(e) => setHook(e.target.value)} />
      </Field>
      <Field label="Subtitle (SRT đã dịch)">
        <textarea className={`${inputCls} font-mono text-xs`} rows={10} value={srtText} onChange={(e) => setSrtText(e.target.value)} />
      </Field>

      <div className="flex gap-2">
        <button onClick={confirm} className="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700">
          Xác nhận & render
        </button>
        <button onClick={onCancel} className="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-100">
          Hủy
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