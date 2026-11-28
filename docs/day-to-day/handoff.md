# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): Mojo optimization and ComfyUI comparison (`j1kd`)

- Optimized the fused tensor-core Mojo projection from 112 ms to **36–38 ms**
  by using `transpose_b` fragment loads, all block threads for tile copies,
  and a 64x32 warp tile. Other tile sizes and K=64 regressed. Correctness
  is unchanged. ComfyUI/PyTorch performs the same projection in **9.2–9.6 ms**.
- Measured MAX with a runtime FP8 weight input at **11–14 ms / 256 MiB**,
  close to cuBLAS without the bound-weight 512 MiB expansion. Direct FP8
  MMA was ruled out for this pass: quantization is only ~0.2 ms, but the
  MMA itself plus scaling is ~1.1 s.
- The optimized two-block DiT takes **49 ms** (was 112 ms), with the same
  cosine 0.999752 / relative L2 0.0226 as built-in MAX. Full details:
  `k8s/apps/comfyui/max-krea2/README.md`.

## Loose threads

- **No full 28-block execution or timing yet.** With other desktop
  processes present, MAX saw ~11 GiB free and model initialization OOMed
  on a final 96 MiB allocation even at the small shape. The FP8 checkpoint
  is 13.1 GB decimal. A clean/headless desktop or explicit streaming is
  required; do not extrapolate a full step from the projection results.
- ComfyUI's full-model references remain **0.95 s/it fp8_scaled** and
  **0.64 s/it int8_convrot**. MAX has no sampler, text encoder, VAE or
  fixed-seed image comparison yet.
- Clean H3 desktop results landed separately in `8a7fa82`; desktop
  `int8_convrot`, 4-step LoRA, ref-to-video and image edit still need
  clean measurements. w1 remains firmware-limited to ~50 W.
- Unused `longhorn-local` claims `ai/comfyui-w1` and `ai/comfyui-w2`
  (~85 GB each) await the owner's delete/keep decision (`e6rx.8`).
- ADR-0050 (any-port TCP) is still Proposed. Its live desktop exposure
  includes ComfyUI on `127.0.0.1:8188` to `admins`.

## Suggested next steps

- Free enough desktop VRAM (ideally headless), then run
  `max-krea2/run.sh parity.py --layers 28 --quant fp8_mojo_tc --runs 3`.
  That produces the missing full-DiT-step comparison; do not start H3.
- If it still OOMs, choose explicit weight streaming or a smaller
  quantization rather than weakening the comparison.
