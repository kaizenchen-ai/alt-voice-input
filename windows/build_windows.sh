#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DIST_DIR="$SCRIPT_DIR/dist"
PAYLOAD_DIR="$SCRIPT_DIR/installer/payload"

mkdir -p "$DIST_DIR" "$PAYLOAD_DIR"

# Extract local Gemini API keys if available (never committed to git)
KEY_POOL_VAL=""
if [ -f "$HOME/.hermes/.env" ]; then
    KEY_POOL_VAL=$(python3 -c '
import os
keys = []
with open(os.path.expanduser("~/.hermes/.env"), "r", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        k, v = k.strip(), v.strip().strip("\"").strip("\x27")
        if k == "KEY_POOL":
            for p in v.split(","):
                p = p.strip()
                if p and p not in keys:
                    keys.append(p)
        elif k in ("GOOGLE_API_KEY_FALLBACK_1", "GOOGLE_API_KEY", "GEMINI_API_KEY"):
            if v and v not in keys:
                keys.append(v)
print(",".join(keys))
')
fi

echo "🔨 [1/2] Cross-compiling AltVoiceInput.exe (Windows 64-bit Native GUI)..."
cd "$SCRIPT_DIR"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
    -ldflags="-H=windowsgui -s -w -X main.buildTimeKeyPool=${KEY_POOL_VAL}" \
    -o "$DIST_DIR/AltVoiceInput.exe" main.go

cp "$DIST_DIR/AltVoiceInput.exe" "$PAYLOAD_DIR/AltVoiceInput.exe"
cp "$PROJECT_ROOT/voice_vocabulary.json" "$PAYLOAD_DIR/voice_vocabulary.json"

echo "📦 [2/2] Cross-compiling AltVoiceInput_Setup.exe (Windows One-Click Installer)..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build \
    -ldflags="-H=windowsgui -s -w" \
    -o "$DIST_DIR/AltVoiceInput_Setup.exe" ./installer

echo "✅ Build complete!"
ls -lh "$DIST_DIR/AltVoiceInput.exe" "$DIST_DIR/AltVoiceInput_Setup.exe"
file "$DIST_DIR/AltVoiceInput.exe" "$DIST_DIR/AltVoiceInput_Setup.exe"
