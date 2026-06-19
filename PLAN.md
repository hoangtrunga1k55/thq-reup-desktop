# Auto ReUp Studio — Desktop App (PLAN)

> Repo mới, **độc lập** với `thq-reup` (web/backend giữ nguyên 100%).
> Mục tiêu: app cài trên máy user, **tự chạy trọn pipeline reup ngay trên máy user** (Hướng A) — gọi thẳng các API bên thứ ba giống hệt backend web đang gọi, và làm cục bộ 2 tác vụ nặng: **OCR xác định vùng subtitle** + **render video (FFmpeg)**. Ưu tiên **Windows**, macOS là bonus.

---

## 1. Quyết định kiến trúc (đã chốt với owner)

| Vấn đề | Quyết định |
|--------|-----------|
| Pipeline chạy ở đâu | **Toàn bộ trên máy user** (download, OCR, transcribe, translate, AI content, TTS, render, post FB). Backend KHÔNG xử lý job của desktop user. |
| Vai trò backend `thq-reup` | Chỉ **auth + license** (giữ quyền kiểm soát SaaS) + kéo settings không bí mật. **Không sửa backend.** |
| API key bên thứ ba | Backend trả key write-only (đã mã hoá, không lấy được). → App **tự cho user nhập key & lưu cục bộ** (OS keychain / mã hoá tại chỗ). |
| OCR engine | **RapidOCR (ONNX runtime)** thay PaddlePaddle — cùng model PP-OCR nhưng nhẹ, không cần Python paddle, chạy ngon trên Windows (không lỗi AVX-512). |
| OS | **Windows trước**; macOS nếu chạy được. |
| Lịch sử job | MVP lưu **cục bộ (SQLite trong app)**. Đồng bộ lên web để sau (cần thêm endpoint backend). |

> Hệ quả: với desktop user, web dashboard và app là **2 lịch sử tách biệt** ở MVP. Có thể đồng bộ sau bằng 1 endpoint backend nhỏ (ngoài phạm vi MVP để giữ thq-reup nguyên vẹn).

---

## 2. Tech stack đề xuất

| Lớp | Công nghệ | Lý do |
|-----|-----------|-------|
| Shell desktop + UI | **Tauri 2** + React + TS + Tailwind (port lại component từ `thq-reup/frontend`) | Nhẹ (~10MB shell), RAM thấp khi chạy render nền lâu; UI giống web hiện tại, tái dùng được code React |
| **Pipeline engine** | **Go sidecar binary** | **Tái dùng gần như verbatim** các package Go đã chiến đấu thực tế: `services/thq`, `services/srtvoice`, `services/openai`, `services/facebook`, `services/ffmpeg`. Các Client này đã nhận `apiKey` làm tham số → bê thẳng sang. |
| OCR | **RapidOCR-onnxruntime** (sidecar nhỏ, localhost `/detect`) | Giữ nguyên contract HTTP `/detect` → tái dùng luôn `detect.go` (logic clustering/scoring) |
| FFmpeg | Binary bundle theo OS + hardware accel | Windows: `h264_nvenc`/`h264_qsv`/`libx264`; macOS: `videotoolbox` |
| Local DB | SQLite | Lịch sử job, cấu hình |
| Key storage | OS keychain (Windows Credential Manager / macOS Keychain) | Không để key plaintext trên đĩa |

**Mô hình tiến trình:** Tauri (UI) ⇄ Go sidecar (orchestrator pipeline) → spawn OCR sidecar (localhost) + gọi ffmpeg + gọi API bên thứ ba. Tauri ↔ Go giao tiếp qua stdin/stdout JSON-lines hoặc localhost HTTP; progress đẩy ngược lên UI realtime (thay thế SSE của web).

> **Vì sao Go sidecar mà không viết lại bằng Rust:** toàn bộ logic adapter + ffmpeg + SRT grouping + translate retry + OCR scoring đã tồn tại và đã test trong Go. Viết lại Rust = port + maintain song song + dễ lệch hành vi. Go sidecar = reuse, rủi ro thấp nhất.

---

## 3. Pipeline phải tái hiện (map đầy đủ từ `thq-reup`)

Thứ tự 3 stage (giữ cả **Auto mode** và **Manual mode** — user xác nhận vùng sub & content giữa chừng):

### Stage 1 — Download + OCR
1. **Download**: `thq.Client.ParseAndDownload(apiKey, url)` → THQ Tools.
   - Detect platform từ URL (TikTok `/api/tiktok/video/download`, Douyin `/api/video/download`); Facebook **không hỗ trợ**.
   - Submit → nhận `jobId` → poll `GET /api/job/:jobId` tới `state=completed` → `download_url` → tải file về `video.mp4`.
2. **NormalizeToFullHD**: ffmpeg scale/pad về **1080×1920** (no-op nếu đã đúng). [LOCAL — CPU]
3. **OCR detect vùng sub** [LOCAL — tác vụ nặng #1]:
   - Lấy tối đa **10 frame ngẫu nhiên** trong khoảng 5%–95% thời lượng (`ExtractFrame`).
   - Mỗi frame → POST `/detect` (RapidOCR sidecar) → danh sách `{text, conf, x,y,w,h}`.
   - Lọc & chấm điểm (port từ `detect.go`): bỏ conf<0.5, bỏ text 1 ký tự, bỏ vùng trên (centerY<45%); phạt nặng vùng >92% (watermark); reject >95%; `score = rune_count*(0.3+posMul)`.
   - **Voting/clustering** (port từ `process_job.go`): gom candidate theo tâm dọc, tolerance 6% chiều cao, chọn cluster nhiều phiếu nhất (hoà → ưu tiên band dưới).
   - Scale box về kích thước video + padding (2% ngang, 6px dọc). Fallback: `DefaultSubtitleRegion` (70% từ trên, rộng 90%, cao 10%).
   - **Manual mode**: dừng, hiện frame + box cho user kéo/chỉnh → user xác nhận.

### Stage 2 — Audio → Sub → Content
4. **ExtractAudio**: ffmpeg → `audio.mp3`. [LOCAL]
5. **Transcribe**: `openai.TranscribeAudio` → Whisper `whisper-1`, `response_format=verbose_json`, `timestamp_granularities[]=word` → gom word thành SRT (max 10 từ / 4.5s / split gap>0.6s / cuối câu).
6. **Translate**: `openai.TranslateSubtitle` → GPT-4o, dịch theo **chỉ mục `[[n]] (Xs)`** giữ nguyên timestamp & số block (logic chống lệch timeline + retry 2 lần block chưa dịch). *(Prompt verbatim đã có — port nguyên.)*
7. **AI content**: `openai.GenerateAIContent` → GPT-4o JSON (title, short_description, caption, hashtags[8-12], title_variants[3]) theo tone; `GenerateHookText` → GPT-4o-mini (hook ≤2 dòng).
   - **Manual mode**: dừng cho user sửa content/sub.

### Stage 3 — Voice → Render → Post
8. **TTS**: `srtvoice.ConvertAndDownload` → multipart `/api/convert` (provider/voice/rate/strategy/speed_adjustment) → poll `/api/status/:id` → `GET /api/download/:id` → `voice.mp3`. Có progress callback.
9. **Render** [LOCAL — tác vụ nặng #2]: `ffmpeg.RenderFinalVideo(RenderConfig)` → `output.mp4`.
   - filter_complex: zoom/scale/pad/crop → flip → **drawbox che sub cũ** → **burn sub mới (ASS)** → hook text → brand watermark; mix audio gốc (vol 0.15) + voice (vol 1.8); `libx264 veryfast crf20` + aac.
   - **Bỏ render-slot semaphore** của server (vô nghĩa trên máy 1 user) — máy user render tuần tự/song song tuỳ cấu hình.
10. **Post Facebook** (nếu bật): `facebook.UploadVideo` (graph-video `/{page}/videos` multipart) + `PostComment`.
11. **Complete**: lưu lịch sử local, hiện link/preview, cho download.

---

## 4. API contract bên thứ ba (đã xác minh — để engine gọi thẳng)

| Service | Base URL | Luồng |
|---------|----------|-------|
| THQ Tools | `https://thq-solution-tools.io.vn` | `POST /api/{tiktok/}video/download` (Bearer) → `GET /api/job/:id` poll `state` |
| SRT-To-Voice | `https://srt-to-voice.io.vn` | `POST /api/convert` (multipart, Bearer+token) → `GET /api/status/:id` → `GET /api/download/:id`; `GET /api/voices` |
| OpenAI | `https://api.openai.com/v1` | `/audio/transcriptions` (whisper-1, verbose_json, word ts); `/chat/completions` (gpt-4o, temp 0.7, json_object cho content) |
| Facebook | `graph(-video).facebook.com/v18.0` | `POST /{page}/videos` (multipart source); `POST /{post}/comments`; `POST /{page}/feed` |

> Tất cả Client Go cho 4 service trên **đã có sẵn** trong `thq-reup/backend/internal/services/` và nhận `apiKey` tham số → copy sang repo desktop, gần như không sửa.

---

## 5. Cấu trúc repo mới `thq-reup-desktop`

```
thq-reup-desktop/
├── PLAN.md
├── src-tauri/                 # Tauri shell (Rust)
│   ├── tauri.conf.json        # bundle ffmpeg + sidecars + updater
│   └── src/                   # spawn/giám sát sidecar, IPC, keychain
├── src/                       # React UI (port từ thq-reup/frontend)
│   ├── pages/                 # Login, Dashboard, NewJob, JobDetail, Settings, Keys
│   ├── components/            # JobCard, ProgressStepper, SubtitleAreaEditor, VideoPreview, AIContentEditor, LogViewer
│   └── lib/                   # client gọi backend (auth/license/settings) + client gọi Go sidecar
├── engine/                    # Go sidecar — pipeline engine
│   ├── cmd/engine/main.go     # orchestrator + IPC server
│   ├── pipeline/              # port process_job.go (3 stage, auto/manual)
│   ├── services/              # COPY: thq, srtvoice, openai, facebook, ffmpeg
│   ├── ocr/                   # detect.go (port) gọi RapidOCR sidecar
│   └── store/                 # SQLite lịch sử + settings local
├── ocr-sidecar/               # RapidOCR-onnxruntime, /detect (PyInstaller hoặc binary)
└── resources/                 # ffmpeg binaries, fonts (Noto Sans/CJK/Thai, DejaVu), onnx models
```

**Fonts:** bundle đúng bộ font web dùng (Noto Sans/Bold, Noto CJK, Noto Thai, DejaVu). Sửa `fontFiles` map từ path container → path resource của app.

---

## 6. UI (đơn giản, đầy đủ, giống web)

Màn hình tối thiểu (bám theo `frontend/app` của web):
- **Login** → backend `/api/auth/login` + check `/api/me/license`.
- **Settings → API Keys**: nhập OpenAI / THQ / SRT-Voice / Facebook token → lưu keychain. (Kéo default không bí mật từ `GET /api/settings`.)
- **Settings → Defaults**: font/size/màu/stroke, cover màu/opacity, brand (text/image), flip/zoom/volume, hook, voice/provider/speed, target language, auto/manual mode, auto-post.
- **Dashboard**: danh sách job local + trạng thái + progress.
- **New Job**: nhập URL TikTok/Douyin → chọn target language + voice → Start.
- **Job Detail**: ProgressStepper realtime; **SubtitleAreaEditor** (kéo/chỉnh box ở manual mode); **AIContentEditor** (sửa title/caption/hashtags); SubtitleEditor (sửa SRT); VideoPreview + Download; nút Post Facebook.

---

## 7. Lộ trình triển khai

### Phase 0 — Khởi tạo & engine xương sống
- Tauri 2 project (target Windows) + React UI scaffold (port layout/login từ web).
- Go engine skeleton: copy 4 service package + ffmpeg service; IPC stdout-JSON; SQLite store.
- Login + license check + kéo settings từ backend; màn nhập key → keychain.

### Phase 1 — Pipeline Auto mode (chạy được end-to-end, không cần OCR đẹp)
- Port `process_job.go` 3 stage chạy tuần tự trong engine (dùng `DefaultSubtitleRegion` tạm cho OCR).
- Progress realtime lên UI. Render local + Download. Post FB.

### Phase 2 — OCR local + Manual mode
- RapidOCR sidecar `/detect`; port `detect.go` + clustering; SubtitleAreaEditor cho user chỉnh.
- Manual confirm subtitle + content.

### Phase 3 — Hoàn thiện Windows + đóng gói
- Hardware accel detect; bundle ffmpeg/fonts/models; Tauri updater; installer (NSIS/MSI); code signing.

### Phase 4 (optional) — macOS + đồng bộ lịch sử lên web
- Build mac (videotoolbox, notarization).
- (Cần endpoint backend mới) push job record để web dashboard thấy.

---

## 8. Rủi ro & xử lý

| Rủi ro | Xử lý |
|--------|-------|
| Key bên thứ ba nằm trên máy user | Lưu OS keychain, không log; chấp nhận theo Hướng A. Backend vẫn gác license. |
| OCR nặng/khó đóng gói trên Windows | RapidOCR-onnx (không paddle); nếu vẫn lớn → port detect/recog sang onnxruntime-go bỏ Python (Phase sau). |
| Bundle lớn (ffmpeg+onnx+models) | ~300–500MB — chấp nhận với app creator; tách model tải lần đầu nếu cần. |
| Máy user yếu/không GPU | Tự dò encoder, fallback `libx264`; cho chọn preset nhanh. |
| Lệch hành vi so với web | Tái dùng code Go verbatim; golden test so output (ffprobe resolution/duration/streams). |
| Lịch sử tách biệt web/app | Chấp nhận ở MVP; đồng bộ là Phase 4. |

---

## 9. Câu hỏi còn mở (xác nhận trước khi scaffold)

1. **Key**: OK phương án user tự nhập key trong app + lưu keychain (đúng Hướng A)? Hay muốn mình thêm 1 endpoint export key ở backend (sẽ phải sửa thq-reup chút)?
2. **Engine = Go sidecar reuse** (khuyến nghị) — đồng ý chứ?
3. **OCR = RapidOCR-onnx sidecar** — đồng ý, hay muốn port hẳn sang onnxruntime-go ngay (bỏ Python, lâu hơn)?
4. **Lịch sử job**: MVP để local riêng cho app — OK chứ?
```