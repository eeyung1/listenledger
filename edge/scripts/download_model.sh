#!/usr/bin/env bash
# ListenLedger Edge — Download quantized Whisper model weights.
# Usage: ./download_model.sh [tiny.en|base.en] [target_dir]

set -euo pipefail

MODEL="${1:-tiny.en}"
TARGET_DIR="${2:-$(dirname "$0")/../models}"
mkdir -p "$TARGET_DIR"

declare -A MODEL_URLS=(
  ["tiny.en"]="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-tiny.en.bin"
  ["base.en"]="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"
)

declare -A MODEL_SHA256=(
  ["tiny.en"]="bd577a113a8644452428582320ed3a0623f9fbb0f407b3287c3466cb50a5670e"
  ["base.en"]="46514b5637c29da34e3794543f6fa0c604f66721ab5552db78077e0ab84d3903"
)

if [[ -z "${MODEL_URLS[$MODEL]:-}" ]]; then
  echo "Error: Unknown model '$MODEL'. Available models: tiny.en, base.en" >&2
  exit 1
fi

URL="${MODEL_URLS[$MODEL]}"
EXPECTED_SHA="${MODEL_SHA256[$MODEL]}"
TARGET_FILE="$TARGET_DIR/ggml-$MODEL.bin"

echo "=== ListenLedger Edge Model Downloader ==="
echo "Model:        $MODEL"
echo "Destination:  $TARGET_FILE"
echo "URL:          $URL"

if [[ -f "$TARGET_FILE" ]]; then
  echo "File already exists. Verifying checksum..."
  ACTUAL_SHA=$(sha256sum "$TARGET_FILE" | awk '{print $1}')
  if [[ "$ACTUAL_SHA" == "$EXPECTED_SHA" ]]; then
    echo "Checksum OK ($ACTUAL_SHA). Model is ready for pilot use."
    exit 0
  else
    echo "Checksum mismatch! Existing file is invalid. Re-downloading..."
    rm -f "$TARGET_FILE"
  fi
fi

echo "Downloading $MODEL weights..."
if command -v curl >/dev/null 2>&1; then
  curl -L -f --progress-bar "$URL" -o "$TARGET_FILE"
elif command -v wget >/dev/null 2>&1; then
  wget -q --show-progress "$URL" -O "$TARGET_FILE"
else
  echo "Error: curl or wget is required to download model weights." >&2
  exit 1
fi

echo "Verifying checksum..."
ACTUAL_SHA=$(sha256sum "$TARGET_FILE" | awk '{print $1}')
if [[ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]]; then
  echo "Error: Checksum verification failed!" >&2
  echo "Expected: $EXPECTED_SHA" >&2
  echo "Actual:   $ACTUAL_SHA" >&2
  rm -f "$TARGET_FILE"
  exit 1
fi

echo "Model downloaded and verified successfully."
echo "Configured path: $TARGET_FILE"
