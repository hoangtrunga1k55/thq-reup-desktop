# Build & chạy trên Windows (target chính)

Hướng dẫn build app desktop + chạy thử end-to-end trên Windows.

## 1. Prerequisites

| Công cụ | Ghi chú |
|---------|---------|
| **Node 18+** | `node -v` |
| **Rust (rustup)** + **MSVC Build Tools** | https://www.rust-lang.org/tools/install — chọn `x86_64-pc-windows-msvc`; cài "Desktop development with C++" của VS Build Tools |
| **Go 1.22+** | `go version` |
| **Python 3.10** | để build OCR sidecar (PyInstaller) |
| **WebView2 Runtime** | thường có sẵn trên Win10/11 |
| **ffmpeg + ffprobe** | đặt vào `src-tauri\resources\ffmpeg\` (hoặc để trong PATH — engine tự fallback) |

## 2. Cài JS deps

```powershell
npm install
```

## 3. Build engine sidecar (Go)

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build-engine.ps1
# -> src-tauri\binaries\engine-x86_64-pc-windows-msvc.exe
```

## 4. Build OCR sidecar (RapidOCR → 1 file .exe)

```powershell
cd ocr-sidecar
python -m venv .venv
.venv\Scripts\activate
pip install -r requirements.txt pyinstaller
pyinstaller --onefile --name ocr-sidecar app.py
copy dist\ocr-sidecar.exe ..\src-tauri\binaries\ocr-sidecar-x86_64-pc-windows-msvc.exe
cd ..
```

> Lần chạy đầu RapidOCR tải model ONNX (vài chục MB). Để đóng gói offline, bundle model vào `--add-data` (xem docs RapidOCR).

## 5. ffmpeg (tùy chọn bundle)

Đặt `ffmpeg.exe` và `ffprobe.exe` vào `src-tauri\resources\ffmpeg\`. Nếu bỏ qua, engine dùng `ffmpeg`/`ffprobe` trong PATH.

## 5b. Fonts (cho hook/brand text + phụ đề)

Đặt các file font sau vào `src-tauri\resources\fonts\` (engine resolve theo tên file, hoạt động trên mọi OS):

| Tên file | Dùng cho |
|----------|----------|
| `NotoSans-Regular.ttf`, `NotoSans-Bold.ttf` | Latin (Việt/Anh…) |
| `NotoSansCJK-Bold.ttc`, `NotoSansCJK-Regular.ttc` | Hàn/Trung/Nhật |
| `NotoSansThai-ExtraBold.ttf` | Thái |
| `DejaVuSans.ttf`, `DejaVuSans-Bold.ttf` | fallback |

> Nếu thiếu file, drawtext (hook/brand) tự fallback sang `font=<tên>` qua fontconfig — cần font cài sẵn trên máy. Phụ đề ASS dùng tên font (libass) nên vẫn cần font Noto khả dụng.

## 6. Cấu hình backend

Sửa `src/lib/backend.ts` → `BASE_URL` trỏ tới host backend `thq-reup` thật (để đăng nhập + license).

## 7. Chạy dev

```powershell
npm run tauri dev
```

## 8. Build installer

```powershell
npm run tauri build
# Installer NSIS: src-tauri\target\release\bundle\nsis\*.exe
```

## 8b. Build tự động bằng GitHub Actions (không cần máy Windows)

Workflow `.github/workflows/build-windows.yml` chạy trên `windows-latest`: build engine (Go) + OCR sidecar (PyInstaller) + Tauri NSIS, rồi upload installer làm artifact.

- Chạy tay: tab **Actions → Build Windows → Run workflow**.
- Tự chạy khi push tag `v*` (vd `git tag v0.1.0 && git push --tags`).
- Tải installer ở mục **Artifacts** của lần chạy (`auto-reup-windows-installer`).

> Bước build OCR sidecar dùng `--collect-all rapidocr_onnxruntime` để gom model ONNX; nếu thiếu data có thể cần chỉnh thêm `--collect-data`.

---

## Checklist verify end-to-end

1. [ ] App mở, đăng nhập bằng tài khoản backend (license active).
2. [ ] Mục **API Keys**: nhập OpenAI / THQ / SRT-Voice (+ Facebook token nếu cần) → lưu (keychain).
3. [ ] **Tạo job** (auto mode) với 1 link TikTok/Douyin → theo dõi tiến trình: download → OCR → audio → transcribe → translate → content → voice → render.
4. [ ] Video kết quả phát được trong JobDetail; file ở `%APPDATA%\vn.thqsolution.autoreup.desktop\jobs\<id>\output.mp4`.
5. [ ] **Manual mode**: tạo job có tick "Chế độ thủ công" → kéo chỉnh vùng subtitle → sửa content/SRT → render.
6. [ ] (Tùy chọn) Bật auto-post Facebook → kiểm tra bài đăng.
7. [ ] Dashboard hiện lịch sử job (SQLite local).

---

## Known gaps (Phase 3 follow-up)

- **Font cho hook/brand text**: ĐÃ FIX — engine resolve font từ `--fonts-dir` (Tauri truyền `src-tauri\resources\fonts`), escape path an toàn cho Windows (`C:\`), fallback `font=<tên>` qua fontconfig nếu thiếu file. Chỉ cần đặt đúng file font ở mục 5b.
- **Auto-update** (Tauri updater) chưa cấu hình (cần key ký). Thêm khi phát hành.
- **Code signing**: installer chưa ký → Windows SmartScreen cảnh báo. Ký EV/OV cert khi phát hành.