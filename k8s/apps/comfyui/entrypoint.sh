#!/bin/sh
# In-cluster start. The image is the stock uv+python image and carries
# NOTHING of ours: the ComfyUI checkout, the venv (torch + CUDA wheels,
# ~7GB) and the models all live on the node-local volume at /data, and
# this script brings that volume to what versions.env + requirements.lock
# (mounted at /app from the ConfigMap) say. A warm start does two file
# compares and execs; only a version bump downloads anything, and then
# only the wheels that changed.
set -eu
app=/app
. "$app/versions.env"
home=${COMFY_HOME:-/data}
export HOME="$home" UV_CACHE_DIR="$home/.uv-cache" UV_LINK_MODE=hardlink UV_PYTHON_DOWNLOADS=never
# uv's 30s default killed the first install on w1 (2026-11-17): a 1GB
# wheel stalled while the WAN was busy and the container restarted. The
# cache is on the volume, so a retry resumes — but do not need one.
export UV_HTTP_TIMEOUT=600
mkdir -p "$home/models" "$home/output" "$home/input" "$home/user"

if [ "$(cat "$home/ComfyUI/.ref" 2>/dev/null || true)" != "$COMFYUI_REF" ]; then
  echo "entrypoint: fetching ComfyUI $COMFYUI_REF"
  rm -rf "$home/ComfyUI.new" && mkdir "$home/ComfyUI.new"
  python3 - "$COMFYUI_REPO/archive/refs/tags/$COMFYUI_REF.tar.gz" "$home/ComfyUI.new" <<'PY'
import sys, tarfile, urllib.request
url, dst = sys.argv[1:]
with urllib.request.urlopen(url) as r, tarfile.open(fileobj=r, mode="r|gz") as t:
    for m in t:
        m.name = m.name.split("/", 1)[1] if "/" in m.name else ""
        if m.name:
            t.extract(m, dst, filter="data")
PY
  echo "$COMFYUI_REF" > "$home/ComfyUI.new/.ref"
  rm -rf "$home/ComfyUI" && mv "$home/ComfyUI.new" "$home/ComfyUI"
fi

want=$(sha256sum "$app/requirements.lock" | cut -d' ' -f1)
if [ "$(cat "$home/venv/.lock-sha" 2>/dev/null || true)" != "$want" ]; then
  echo "entrypoint: syncing venv to requirements.lock ($want)"
  [ -x "$home/venv/bin/python" ] || uv venv -q --python python3 "$home/venv"
  uv pip sync --python "$home/venv/bin/python" --torch-backend "$TORCH_BACKEND" "$app/requirements.lock"
  echo "$want" > "$home/venv/.lock-sha"
fi
COMFY_HOME="$home" exec bash "$app/run.sh" "$@"
