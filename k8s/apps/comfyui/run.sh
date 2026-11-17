#!/usr/bin/env bash
# Start ComfyUI against a model tree that lives OUTSIDE the checkout
# (desktop: a data disk; cluster: the model PVC at /models). One
# launcher for both, so the benchmark compares GPUs, not flags.
#   COMFY_HOME  dir holding ComfyUI/ venv/ output/ input/ user/
#   MODELS      model tree (default $COMFY_HOME/models), ComfyUI layout
#   LISTEN/PORT default 127.0.0.1:8188
# Extra args go to main.py verbatim.
set -euo pipefail
paths_only=
if [ "${1:-}" = --write-paths-only ]; then paths_only=1; COMFY_HOME=$2; shift 2; fi
home=${COMFY_HOME:-$HOME/disks/1TB-2024/ai}
models=${MODELS:-$home/models}
cat > "$home/extra_model_paths.yaml" <<YAML
models:
  base_path: $models
  is_default: true
  diffusion_models: diffusion_models
  text_encoders: text_encoders
  vae: vae
  loras: loras
  model_patches: model_patches
  embeddings: embeddings
  checkpoints: checkpoints
YAML
[ -n "$paths_only" ] && exit 0
py=$home/venv/bin/python; [ -x "$py" ] || py=python
# torch 2.14 JIT-compiles some ops with triton, which finds libcuda by
# running /sbin/ldconfig -p. NixOS has neither that binary nor an
# ld.so.cache; the driver's libraries are under /run/opengl-driver.
# (Debian in the pod has ldconfig, and the nvidia runtime registers
# the injected libcuda there.)
if [ ! -x /sbin/ldconfig ] && [ -e /run/opengl-driver/lib/libcuda.so.1 ]; then
  export TRITON_LIBCUDA_PATH=/run/opengl-driver/lib
fi
cd "$home/ComfyUI"
exec "$py" main.py --listen "${LISTEN:-127.0.0.1}" --port "${PORT:-8188}" \
  --extra-model-paths-config "$home/extra_model_paths.yaml" \
  --output-directory "$home/output" --input-directory "$home/input" \
  --user-directory "$home/user" "$@"
