#!/bin/bash
set -e

# Alt Voice Input - One-Click Installer for macOS
echo "=== Installing Alt Voice Input ==="

# 1. Check Python 3
if ! command -v python3 &>/dev/null; then
    echo "❌ Python 3 is required. Please install via Homebrew: brew install python"
    exit 1
fi

# 2. Check ffmpeg
if ! command -v ffmpeg &>/dev/null; then
    echo "📦 Installing ffmpeg via Homebrew..."
    brew install ffmpeg
fi

# 3. Check and install Python dependencies
echo "📦 Installing Python dependencies (pynput, pyobjc)..."
python3 -m pip install --user --break-system-packages pynput pyobjc

# 4. Install files to target locations
TARGET_SCRIPT_DIR="$HOME/.hermes/scripts"
TARGET_BIN_DIR="$HOME/.local/bin"
mkdir -p "$TARGET_SCRIPT_DIR" "$TARGET_BIN_DIR"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "📂 Installing alt_voice_input.py to $TARGET_SCRIPT_DIR..."
cp "$SCRIPT_DIR/alt_voice_input.py" "$TARGET_SCRIPT_DIR/alt_voice_input.py"
chmod +x "$TARGET_SCRIPT_DIR/alt_voice_input.py"

echo "📂 Installing alt-voice CLI to $TARGET_BIN_DIR..."
cp "$SCRIPT_DIR/bin/alt-voice" "$TARGET_BIN_DIR/alt-voice"
chmod +x "$TARGET_BIN_DIR/alt-voice"

# 5. Check API key
if [ ! -f "$HOME/.hermes/.env" ] && [ ! -f "$SCRIPT_DIR/.env" ] && [ -z "$GOOGLE_API_KEY" ]; then
    echo "⚠️ Warning: No Gemini API key detected."
    echo "👉 Please set GOOGLE_API_KEY in ~/.hermes/.env or export it in your shell."
    echo "   Free key: https://aistudio.google.com/app/apikey"
fi

echo "✅ Installation complete!"
echo "👉 Start service: alt-voice start"
echo "👉 Check status: alt-voice status"
echo "👉 Stop service: alt-voice stop"
