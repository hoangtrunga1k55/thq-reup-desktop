// In-memory cache of the user's credentials (keys + settings) fetched from the
// backend after login. Nothing is persisted on disk — the exe is zero-config.

import { getCredentials, type Credentials } from "./backend";

let cache: Credentials | null = null;

export async function loadCredentials(force = false): Promise<Credentials> {
  if (cache && !force) return cache;
  cache = await getCredentials();
  return cache;
}

export function cachedCredentials(): Credentials | null {
  return cache;
}

export function clearCredentials() {
  cache = null;
}