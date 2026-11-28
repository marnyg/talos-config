# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): protocol network + Krea 2 NVFP4

- Protocol network decisions are Accepted: ADR-0051 and protocol ADR-0012.
  Built and tested the relay gate, child presentation, supervised lighthouse
  binary and fly app (`qx5c`, `gsps`, `9nkh`). The app is not deployed and
  the running system is unchanged.
- Krea 2 (`j1kd`) now supports mixed NVFP4: native MAX W4A4 plus a packed
  custom W4A16 kernel. The full 28-block DiT at 1024² takes **3.21 s/step**
  (FP8 was 6.41 s) and fits **with the desktop active**, retaining 5.0 GiB
  of MAX pool after setup.
- Real ComfyUI NVFP4 GPU references give full-model cosine **0.993576**
  against normal dynamic-scale output and **0.997914** against unit-scale
  output; two-block parity is 0.999766. FP8 and multi-tile NVFP4 tests pass.
- ComfyUI remains faster (0.90 s/it NVFP4, 0.64 s/it int8_convrot) and is the
  tool to use. MAX remains a DiT-only port, not an image pipeline.

## Loose threads

- Relay chain links do not feed the clock mark (`w0ur`); the relay bearer id
  is unsigned. Foreign-relay dialing, per-client byte limits and fly-proxy
  testing remain open.
- Krea 2: MAX's native NVFP4 API requires a host global activation scale, so
  it cannot reproduce ComfyUI's computed device scale. There is still no
  28-block FP32/FP8 reference, sampler, text encoder or VAE.
- **Desktop ComfyUI is stopped** and is not a service; restart with
  `k8s/apps/comfyui/run.sh`.
- Carried over: ADR-0050 is Proposed; unused `longhorn-local` claims await
  the owner (`e6rx.8`).

## Suggested next steps

- Deploy `9nkh`, then `ssau` (move the sap plane) and `e7f0` (enforce the
  hub's member gate).
- Close `j1kd`: porting and the NVFP4 continuation are answered. Further
  kernel/global-scale work needs a new measured goal.
