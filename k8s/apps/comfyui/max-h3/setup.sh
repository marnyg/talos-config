#!/usr/bin/env bash
# Recreate the desktop MAX/Mojo environment without touching ComfyUI's venv.
set -euo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
VENV=${MAX_VENV:-"$HOME/disks/1TB-2024/ai/max-venv"}

uv venv --python 3.13 "$VENV"
uv pip install --python "$VENV/bin/python" -r "$HERE/requirements.lock"

printf 'MAX:  %s\n' "$($VENV/bin/max --version)"
printf 'Mojo: %s\n' "$($VENV/bin/mojo --version)"
