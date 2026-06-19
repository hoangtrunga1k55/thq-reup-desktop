// Client for the existing thq-reup backend. The desktop app only uses it for
// auth + license (the SaaS control point) and to pull non-secret default
// settings. It never fetches API keys (the backend stores them write-only).
//
// Uses the Tauri HTTP plugin's fetch so requests bypass webview CORS.

import { fetch } from "@tauri-apps/plugin-http";

// TODO(config): make this user-configurable in Settings. Default to production.
const BASE_URL = "https://api.autoreup.example"; // placeholder — set real backend host

let authToken: string | null = null;

export function setToken(token: string | null) {
  authToken = token;
  if (token) localStorage.setItem("auth_token", token);
  else localStorage.removeItem("auth_token");
}

export function loadToken(): string | null {
  if (!authToken) authToken = localStorage.getItem("auth_token");
  return authToken;
}

async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init.headers as Record<string, string> | undefined),
  };
  const token = loadToken();
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${BASE_URL}${path}`, { ...init, headers });
  const text = await res.text();
  const body = text ? JSON.parse(text) : null;
  if (!res.ok) {
    throw new Error(body?.error || body?.message || `HTTP ${res.status}`);
  }
  return body as T;
}

// ─── Auth ───────────────────────────────────────────────────────────────────

export type LoginResponse = { token: string; user?: { id: number; email: string } };

export async function login(email: string, password: string): Promise<LoginResponse> {
  const res = await api<LoginResponse>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify({ email, password }),
  });
  setToken(res.token);
  return res;
}

export function logout() {
  setToken(null);
}

export type License = { active: boolean; plan?: string; expires_at?: string };

export async function getLicense(): Promise<License> {
  return api<License>("/api/me/license");
}

// ─── Settings (non-secret defaults) ───────────────────────────────────────────
// GET /api/settings returns has_* flags for keys + all non-secret render/voice
// defaults. The desktop app uses the defaults to pre-fill its local settings.

export type BackendSettings = Record<string, unknown> & {
  has_openai_api_key?: boolean;
  has_thq_solution_api_key?: boolean;
  has_srt_to_voice_api_key?: boolean;
  has_facebook_access_token?: boolean;
};

export async function getSettings(): Promise<BackendSettings> {
  return api<BackendSettings>("/api/settings");
}
