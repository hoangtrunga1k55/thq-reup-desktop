#!/usr/bin/env bash
# Build the Go engine sidecar for Tauri (macOS/Linux).
# Output: src-tauri/binaries/engine-<target-triple>[.exe]  (name Tauri expects)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Tauri names sidecars by the Rust host target triple.
TRIPLE="$(rustc -Vv 2>/dev/null | sed -n 's/^host: //p' || true)"
if [ -z "$TRIPLE" ]; then
  echo "warning: rustc not found; cannot determine target triple." >&2
  echo "         install Rust or set TRIPLE=... manually." >&2
  TRIPLE="${TRIPLE:-unknown}"
fi

EXT=""
case "$TRIPLE" in *windows*) EXT=".exe" ;; esac

OUT="$ROOT/src-tauri/binaries/engine-$TRIPLE$EXT"
mkdir -p "$(dirname "$OUT")"
( cd "$ROOT/engine" && go build -o "$OUT" ./cmd/engine )
echo "Built $OUT"