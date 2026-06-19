// Thin client for the Go engine sidecar, bridged through the Tauri shell.
//
// Commands are sent via the `engine_send` Tauri command (written to the engine's
// stdin). Events arrive as `engine-event` window events (one JSON line each).
// We use the JOB ID as the command correlation id, so every event for a job
// carries id === jobId and the UI can subscribe by job id directly.

import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";

export type EngineEvent = {
  id: string;
  type:
    | "ack"
    | "progress"
    | "step"
    | "log"
    | "waiting_subtitle"
    | "waiting_content"
    | "completed"
    | "failed"
    | "result"
    | "error";
  payload: any;
};

type Handler = (e: EngineEvent) => void;

const handlers = new Map<string, Set<Handler>>();
const buffers = new Map<string, EngineEvent[]>(); // replay for late subscribers
const BUFFER_CAP = 500;
let started = false;
let counter = 0;

// Begin listening for engine events. Call once at app startup.
export async function initEngineBridge(): Promise<void> {
  if (started) return;
  started = true;
  await listen<{ line: string }>("engine-event", (evt) => {
    let parsed: EngineEvent;
    try {
      parsed = JSON.parse(evt.payload.line);
    } catch {
      return; // non-JSON stderr noise
    }
    // Buffer by id for replay.
    const buf = buffers.get(parsed.id) ?? [];
    buf.push(parsed);
    if (buf.length > BUFFER_CAP) buf.shift();
    buffers.set(parsed.id, buf);

    handlers.get(parsed.id)?.forEach((h) => h(parsed));
    handlers.get("*")?.forEach((h) => h(parsed));
  });
}

function nextId(): string {
  counter += 1;
  return `cmd-${Date.now()}-${counter}`;
}

// Send a command to the engine. Pass `id` to use a specific correlation id
// (e.g. the job id); otherwise one is generated. Returns the id used.
export async function sendCommand(type: string, payload: unknown, id?: string): Promise<string> {
  const cid = id ?? nextId();
  await invoke("engine_send", { line: JSON.stringify({ id: cid, type, payload }) });
  return cid;
}

// Request/response style: send a command and resolve on its first result event.
export function requestCommand<T = any>(type: string, payload: unknown, timeoutMs = 15000): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const id = nextId();
    let off: () => void = () => {};
    const timer = setTimeout(() => {
      off();
      reject(new Error("engine timeout"));
    }, timeoutMs);
    off = onEngineEvent(id, (e) => {
      if (e.type === "result") {
        clearTimeout(timer);
        off();
        resolve(e.payload as T);
      } else if (e.type === "error" || e.type === "failed") {
        clearTimeout(timer);
        off();
        reject(new Error(e.payload?.error ?? "engine error"));
      }
    });
    invoke("engine_send", { line: JSON.stringify({ id, type, payload }) }).catch((err) => {
      clearTimeout(timer);
      off();
      reject(err);
    });
  });
}

// Subscribe to events for a specific id (or "*" for all). Buffered events for a
// concrete id are replayed immediately. Returns an unsubscribe fn.
export function onEngineEvent(id: string, handler: Handler): () => void {
  let set = handlers.get(id);
  if (!set) {
    set = new Set();
    handlers.set(id, set);
  }
  set.add(handler);
  if (id !== "*") buffers.get(id)?.forEach((e) => handler(e));
  return () => set!.delete(handler);
}

// Clear the buffered events for a job (e.g. when re-running).
export function clearJobBuffer(jobId: string) {
  buffers.delete(jobId);
}

// ─── High-level job commands (correlation id === jobId) ─────────────────────

export type StartJobInput = {
  source_url: string;
  keys: { openai: string; thq: string; srt_voice: string; facebook_token: string };
  settings: Record<string, unknown>;
};

export function startJob(jobId: string, input: StartJobInput): Promise<string> {
  return sendCommand("start_job", { job_id: jobId, ...input }, jobId);
}

export type Region = { x: number; y: number; width: number; height: number };

export function confirmSubtitle(jobId: string, region: Region): Promise<string> {
  return sendCommand("confirm_subtitle", region, jobId);
}

export function confirmContent(
  jobId: string,
  edit: { translated_srt?: string; ai_content?: unknown; hook_text?: string }
): Promise<string> {
  return sendCommand("confirm_content", edit, jobId);
}

export function cancelJob(jobId: string): Promise<string> {
  return sendCommand("cancel_job", {}, jobId);
}

// ─── Keychain (third-party API keys) ────────────────────────────────────────

export const keychain = {
  set: (account: string, secret: string) => invoke<void>("keychain_set", { account, secret }),
  get: (account: string) => invoke<string>("keychain_get", { account }),
  delete: (account: string) => invoke<void>("keychain_delete", { account }),
};

export const KEY_ACCOUNTS = {
  openai: "openai_api_key",
  thq: "thq_solution_api_key",
  srtVoice: "srt_to_voice_api_key",
  facebook: "facebook_access_token",
} as const;