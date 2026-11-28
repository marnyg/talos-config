#!/usr/bin/env bash
# Run a Python script with MAX's explicitly pinned CUDA libraries visible.
set -euo pipefail

VENV=${MAX_VENV:-"$HOME/disks/1TB-2024/ai/max-venv"}
CUDA_LIB="$VENV/lib/python3.13/site-packages/nvidia/cu13/lib"
export LD_LIBRARY_PATH="$CUDA_LIB${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
exec "$VENV/bin/python" "$@"
