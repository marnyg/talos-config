#!/usr/bin/env bash
# Fill a ComfyUI model tree from models.txt. Resumable and idempotent
# (hf skips complete files), sequential in manifest order so the
# smallest working set of each model arrives first.
#   fetch-models.sh <max-tier> [models-dir]
#   MODEL_MIRROR / MODEL_ORDER: see below.
# HF_TOKEN is honoured if set; none of these repos is gated.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tier=${1:?max tier (1|2)}
models=${2:-${MODELS:-$HOME/disks/1TB-2024/ai/models}}
hf=${HF_CLI:-hf}; command -v "$hf" >/dev/null || hf=$HOME/disks/1TB-2024/ai/venv/bin/hf
mkdir -p "$models"
# MODEL_MIRROR=http://peer:8189 — another node's model tree served over
# the LAN (the pod's `models` container runs http.server on it). The
# WAN is the scarce thing (~13 MB/s for the whole house, a tier is
# 80 GB), so: first take whatever the peer already has; then walk the
# manifest, asking the peer first and Hugging Face second, while a
# background loop sweeps the peer again every minute. With
# MODEL_ORDER=reverse on one of two peers they start from opposite ends
# and each file crosses the WAN about once.
mirror=${MODEL_MIRROR:-}
entries=$(grep -vE '^\s*(#|$)' "$here/models.txt" | awk -v t="$tier" '$1 <= t {print $2, $3}')
from_mirror() { # <path>: 0 = now present
  [ -n "$mirror" ] || return 1
  case $1 in *\**) return 1 ;; esac
  python3 - "$mirror/$1" "$models/$1" <<'PY'
import os, shutil, sys, urllib.request
url, dst = sys.argv[1:]
try:
    with urllib.request.urlopen(url, timeout=20) as r:
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        with open(dst + ".part", "wb") as f:
            shutil.copyfileobj(r, f, 4 << 20)
        want = r.headers.get("Content-Length")
        if want and int(want) != os.path.getsize(dst + ".part"):
            raise OSError("short read")
    os.replace(dst + ".part", dst)
except Exception as e:
    try:
        os.remove(dst + ".part")
    except OSError:
        pass
    sys.exit(1)
PY
}
sweep() { # everything the peer has by now, manifest order
  [ -n "$mirror" ] || return 0
  echo "$all" | while read -r repo path; do
    [ -e "$models/$path" ] && continue
    from_mirror "$path" && echo "== $(date +%T) mirror $path"
  done || true
}
all=$entries
sweep
# ...and keep sweeping while the WAN pulls run: a 16 GB pull takes 20+
# minutes, and the peer finishes other files meanwhile.
if [ -n "$mirror" ]; then
  ( while sleep 60; do sweep; done ) &
  sweeper=$!
  trap 'kill $sweeper 2>/dev/null || true' EXIT
fi
[ "${MODEL_ORDER:-}" = reverse ] && entries=$(echo "$entries" | tac)
echo "$entries" | while read -r repo path; do
  if [ ! -e "$models/$path" ] && from_mirror "$path"; then
    echo "== $(date +%T) mirror $path"
    continue
  fi
  echo "== $(date +%T) $repo $path"
  [ -e "$models/$path" ] || "$hf" download "$repo" --include "$path" --local-dir "$models" --quiet
done
rm -rf "$models/.cache"
echo "== $(date +%T) done: $(du -sh "$models" | cut -f1) in $models"
