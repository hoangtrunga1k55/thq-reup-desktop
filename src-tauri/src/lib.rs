//! Tauri shell for the Auto ReUp Studio desktop app.
//!
//! Responsibilities:
//!  - Spawn the OCR sidecar (RapidOCR, http://127.0.0.1:8000) and the Go engine
//!    sidecar; stream the engine's stdout JSON to the webview as `engine-event`.
//!  - Resolve the bundled ffmpeg/ffprobe and pass their paths to the engine
//!    (falls back to PATH when not bundled).
//!  - Forward command lines from the webview to the engine's stdin.
//!  - Store/read the user's third-party API keys in the OS keychain.

use std::sync::Mutex;

use serde::Serialize;
use tauri::path::BaseDirectory;
use tauri::{Emitter, Manager, State};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// Holds the running sidecar children so we can write to / kill them.
#[derive(Default)]
struct EngineState {
    engine: Mutex<Option<CommandChild>>,
    ocr: Mutex<Option<CommandChild>>,
}

#[derive(Clone, Serialize)]
struct EngineLine {
    line: String,
}

const KEYCHAIN_SERVICE: &str = "vn.thqsolution.autoreup.desktop";

/// Forward one JSON command line to the engine's stdin (newline-delimited).
#[tauri::command]
fn engine_send(state: State<EngineState>, line: String) -> Result<(), String> {
    let mut guard = state.engine.lock().map_err(|e| e.to_string())?;
    let child = guard.as_mut().ok_or("engine not running")?;
    let mut payload = line.into_bytes();
    payload.push(b'\n');
    child.write(&payload).map_err(|e| e.to_string())
}

#[tauri::command]
fn keychain_set(account: String, secret: String) -> Result<(), String> {
    let entry = keyring::Entry::new(KEYCHAIN_SERVICE, &account).map_err(|e| e.to_string())?;
    entry.set_password(&secret).map_err(|e| e.to_string())
}

#[tauri::command]
fn keychain_get(account: String) -> Result<String, String> {
    let entry = keyring::Entry::new(KEYCHAIN_SERVICE, &account).map_err(|e| e.to_string())?;
    match entry.get_password() {
        Ok(s) => Ok(s),
        Err(keyring::Error::NoEntry) => Ok(String::new()),
        Err(e) => Err(e.to_string()),
    }
}

#[tauri::command]
fn keychain_delete(account: String) -> Result<(), String> {
    let entry = keyring::Entry::new(KEYCHAIN_SERVICE, &account).map_err(|e| e.to_string())?;
    match entry.delete_password() {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(e) => Err(e.to_string()),
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_http::init())
        .plugin(tauri_plugin_log::Builder::new().build())
        .manage(EngineState::default())
        .setup(|app| {
            // Non-fatal: open the window even if a sidecar can't start.
            if let Err(e) = spawn_ocr(app.handle()) {
                log::error!("failed to spawn ocr sidecar: {e}");
            }
            if let Err(e) = spawn_engine(app.handle()) {
                log::error!("failed to spawn engine sidecar: {e}");
            }
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            engine_send,
            keychain_set,
            keychain_get,
            keychain_delete
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app_handle, event| {
            // Kill the sidecars on exit so they don't linger and lock their .exe
            // files (which blocks the installer from overwriting them on update).
            if let tauri::RunEvent::ExitRequested { .. } = event {
                let state = app_handle.state::<EngineState>();
                // Take the children out (owned) so the lock guards are released
                // within these statements, before `state` is dropped.
                let engine_child = state.engine.lock().ok().and_then(|mut g| g.take());
                let ocr_child = state.ocr.lock().ok().and_then(|mut g| g.take());
                drop(state);
                if let Some(c) = engine_child {
                    let _ = c.kill();
                }
                if let Some(c) = ocr_child {
                    let _ = c.kill();
                }
            }
        });
}

/// Strip the Windows verbatim prefix (\\?\) — it breaks ffmpeg filter paths and
/// some file opens. No-op on non-verbatim paths and other OSes.
fn strip_verbatim(p: String) -> String {
    p.strip_prefix(r"\\?\").map(|s| s.to_string()).unwrap_or(p)
}

/// Resolve a bundled resource binary, returning its path only if it exists.
fn resource_bin(app: &tauri::AppHandle, rel: &str) -> Option<String> {
    app.path()
        .resolve(rel, BaseDirectory::Resource)
        .ok()
        .filter(|p| p.exists())
        .map(|p| strip_verbatim(p.to_string_lossy().into_owned()))
}

/// Spawn the OCR sidecar (binds 127.0.0.1:8000).
fn spawn_ocr(app: &tauri::AppHandle) -> Result<(), Box<dyn std::error::Error>> {
    let (mut rx, child) = app.shell().sidecar("ocr-sidecar")?.spawn()?;
    app.state::<EngineState>()
        .ocr
        .lock()
        .map_err(|e| e.to_string())?
        .replace(child);

    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            if let CommandEvent::Stderr(b) | CommandEvent::Stdout(b) = event {
                log::info!("ocr: {}", String::from_utf8_lossy(&b));
            }
        }
    });
    Ok(())
}

/// Spawn the engine sidecar and pump its stdout JSON to the webview.
fn spawn_engine(app: &tauri::AppHandle) -> Result<(), Box<dyn std::error::Error>> {
    let data_dir = app
        .path()
        .app_data_dir()
        .map(|p| strip_verbatim(p.to_string_lossy().to_string()))
        .unwrap_or_else(|_| ".".to_string());

    // ffmpeg/ffprobe binary names differ per OS.
    let (ffmpeg_rel, ffprobe_rel) = if cfg!(windows) {
        ("resources/ffmpeg/ffmpeg.exe", "resources/ffmpeg/ffprobe.exe")
    } else {
        ("resources/ffmpeg/ffmpeg", "resources/ffmpeg/ffprobe")
    };

    let mut args: Vec<String> = vec![
        "--data-dir".into(),
        data_dir,
        "--ocr-url".into(),
        "http://127.0.0.1:8000".into(),
    ];
    if let Some(p) = resource_bin(app, ffmpeg_rel) {
        args.push("--ffmpeg".into());
        args.push(p);
    }
    if let Some(p) = resource_bin(app, ffprobe_rel) {
        args.push("--ffprobe".into());
        args.push(p);
    }
    // Bundled fonts dir (for hook/brand drawtext overlays).
    if let Ok(dir) = app.path().resolve("resources/fonts", BaseDirectory::Resource) {
        if dir.exists() {
            args.push("--fonts-dir".into());
            args.push(strip_verbatim(dir.to_string_lossy().into_owned()));
        }
    }

    let (mut rx, child) = app.shell().sidecar("engine")?.args(args).spawn()?;
    app.state::<EngineState>()
        .engine
        .lock()
        .map_err(|e| e.to_string())?
        .replace(child);

    let handle = app.clone();
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(bytes) => {
                    let line = String::from_utf8_lossy(&bytes).to_string();
                    let _ = handle.emit("engine-event", EngineLine { line });
                }
                CommandEvent::Stderr(bytes) => {
                    log::info!("engine: {}", String::from_utf8_lossy(&bytes));
                }
                CommandEvent::Terminated(payload) => {
                    log::warn!("engine terminated: {:?}", payload);
                    break;
                }
                _ => {}
            }
        }
    });
    Ok(())
}