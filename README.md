# Auto ReUp Studio — Desktop

Desktop app cho phép user chạy **trọn pipeline reup ngay trên máy của họ** (Hướng A):
tải video, OCR vùng subtitle, transcribe/translate/AI content, TTS, **render video**,
và đăng Facebook — gọi thẳng các API bên thứ ba bằng key của chính user. Backend
`thq-reup` chỉ dùng cho **đăng nhập + license**. Ưu tiên **Windows**.

> Kế hoạch đầy đủ: [`PLAN.md`](./PLAN.md). Đây là scaffold **Phase 0** (xương sống),
> các stage pipeline được lấp ở Phase 1+.

## Kiến trúc

```
┌── Tauri shell (Rust, src-tauri/) ─────────────────────────────┐
│  • spawn engine sidecar, relay stdout JSON → webview events    │
│  • keychain (lưu API key), tauri-plugin-http (gọi backend)     │
│  React UI (src/) — Vite + React 19 + Tailwind 4                │
└───────────────┬───────────────────────────────────────────────┘
                │ stdin/stdout JSON-lines (engine/ipc)
┌───────────────▼─── Go engine sidecar (engine/) ───────────────┐
│  pipeline/ orchestrator  +  services/ (copy verbatim từ web):  │
│  thq · srtvoice · openai · facebook · ffmpeg  +  ocr/ + store/ │
└───────────────┬───────────────────────────────────────────────┘
                │ HTTP 127.0.0.1:8000
        ┌───────▼─────── OCR sidecar (ocr-sidecar/, RapidOCR ONNX) ──┐
        │  POST /detect — cùng contract với PaddleOCR service cũ      │
        └────────────────────────────────────────────────────────────┘
```

## Cấu trúc

| Thư mục | Nội dung |
|---------|----------|
| `src/` | React UI (pages: Login, Dashboard, NewJob, JobDetail, Settings, Keys) |
| `src-tauri/` | Tauri 2 shell (spawn sidecar, keychain) |
| `engine/` | Go pipeline engine. `services/` copy nguyên từ `thq-reup/backend/internal/services` |
| `ocr-sidecar/` | RapidOCR-onnx FastAPI service |
| `resources/` | ffmpeg binaries, fonts, onnx models (cấp lúc build) |

## Phát triển (dev)

```bash
# 1. JS deps
npm install

# 2. Go engine — build sidecar và đặt vào src-tauri/binaries với đuôi target triple
cd engine && go mod tidy && go build -o ../src-tauri/binaries/engine ./cmd/engine && cd ..
# Windows ví dụ: go build -o ../src-tauri/binaries/engine-x86_64-pc-windows-msvc.exe ./cmd/engine

# 3. OCR sidecar (terminal riêng, Phase 2 sẽ tự spawn)
cd ocr-sidecar && pip install -r requirements.txt && uvicorn app:app --host 127.0.0.1 --port 8000

# 4. Chạy app
npm run tauri dev
```

> **ffmpeg**: đặt binary vào `resources/` (hoặc cài sẵn trên máy dev). Engine nhận
> đường dẫn qua cờ `--ffmpeg`.

## Trạng thái Phase 0

- ✅ Cây repo, Tauri shell, IPC bridge, keychain
- ✅ Go engine: IPC loop, store SQLite, orchestrator + service clients đã copy
- ✅ OCR sidecar RapidOCR (contract `/detect`)
- ✅ UI: login (backend auth), nhập key (keychain), tạo job, theo dõi sự kiện engine
- ⏳ **Phase 1**: lấp 3 stage trong `engine/pipeline` (download→OCR→…→render→post)
- ⏳ **Phase 2**: OCR thật + manual mode (chỉnh vùng sub, duyệt content)
- ⏳ **Phase 3**: đóng gói Windows (installer, code signing, auto-update)

## Cấu hình cần đặt trước khi chạy thật

- `src/lib/backend.ts` → `BASE_URL`: host backend thq-reup thật.