#!/usr/bin/env bash
# Desktop install of the pinned ComfyUI stack (versions.env +
# requirements.lock — the same pair the cluster image is built from).
# Idempotent. Usage: setup-local.sh [dir]   (default $COMFY_HOME or
# ~/disks/1TB-2024/ai). Needs uv and nix-ld (manylinux wheels).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
. "$here/versions.env"
home=${1:-${COMFY_HOME:-$HOME/disks/1TB-2024/ai}}
mkdir -p "$home/models" "$home/output" "$home/input" "$home/user" "$home/logs"
if [ ! -d "$home/ComfyUI/.git" ]; then
  git clone -q --depth 1 --branch "$COMFYUI_REF" "$COMFYUI_REPO" "$home/ComfyUI"
else
  git -C "$home/ComfyUI" fetch -q --depth 1 origin tag "$COMFYUI_REF"
  git -C "$home/ComfyUI" checkout -q "$COMFYUI_REF"
fi
[ -d "$home/venv" ] || uv venv -q --python "$PYTHON" "$home/venv"
uv pip sync -q --python "$home/venv/bin/python" --torch-backend "$TORCH_BACKEND" "$here/requirements.lock"
"$here/run.sh" --write-paths-only "$home"
echo "ok: $home  (start with: COMFY_HOME=$home $here/run.sh)"
