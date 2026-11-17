#!/usr/bin/env bash
# Fill a ComfyUI model tree from models.txt. Resumable and idempotent
# (hf skips complete files), sequential in manifest order so the
# smallest working set of each model arrives first.
#   fetch-models.sh <max-tier> [models-dir]
# HF_TOKEN is honoured if set; none of these repos is gated.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tier=${1:?max tier (1|2)}
models=${2:-${MODELS:-$HOME/disks/1TB-2024/ai/models}}
hf=${HF_CLI:-hf}; command -v "$hf" >/dev/null || hf=$HOME/disks/1TB-2024/ai/venv/bin/hf
mkdir -p "$models"
grep -vE '^\s*(#|$)' "$here/models.txt" | while read -r t repo path _; do
  [ "$t" -le "$tier" ] || continue
  echo "== $(date +%T) $repo $path"
  "$hf" download "$repo" --include "$path" --local-dir "$models" --quiet
done
rm -rf "$models/.cache"
echo "== $(date +%T) done: $(du -sh "$models" | cut -f1) in $models"
