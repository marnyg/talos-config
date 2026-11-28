# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): Krea 2 MAX/Mojo spike (`j1kd`)

- Recreated and pinned the MAX 26.7.0 / Mojo 1.2.0 environment and added
  reproducible setup, test and run wrappers under
  `k8s/apps/comfyui/max-krea2/`. The Python graph frontend now has real
  custom Mojo operations; this is no longer only a MAX built-in-kernel test.
- Added exact FP8 dequantization, a fused scalar tiled GEMM, and a fused
  BF16 tensor-core GEMM. At the real Krea projection shape they measure
  about 10 ms (cuBLAS reference), 953 ms and 112 ms respectively; both
  fused variants use a 256 MiB allocation delta versus 512 MiB for
  dequant+cuBLAS. MAX's native dynamic-FP8 path is much slower again
  (~2.03 s). Details: `max-krea2/README.md`.
- The tensor-core path also runs the two-block Krea DiT with the same
  reported cosine (0.999752) and relative L2 (0.0226) as the built-in
  path against the same FP32 reference. Cached two-block execution is
  112 ms versus 14 ms, so Mojo is a memory-saving experiment, not yet a
  speed win.

## Loose threads

- No full 28-block forward pass, sampler, text encoder, VAE or image
  comparison exists. Fused kernels are GPU-only and require aligned
  shapes; tensor-core M/N must be divisible by 64 and K by 32.
- Further Mojo performance work is pipelining/register tiling and
  removing synchronous tile copies. Do not expand this to H3 or the
  8 GB cards unless the Krea trade-off justifies it.
- Clean H3 desktop results landed separately in `8a7fa82`; desktop
  `int8_convrot`, 4-step LoRA, ref-to-video and image edit still need
  clean measurements. w1 remains firmware-limited to ~50 W.
- Unused `longhorn-local` claims `ai/comfyui-w1` and `ai/comfyui-w2`
  (~85 GB each) await the owner's delete/keep decision (`e6rx.8`).
- ADR-0050 (any-port TCP) is still Proposed. Its live desktop exposure
  includes ComfyUI on `127.0.0.1:8188` to `admins`.

## Suggested next steps

- Decide whether the fused kernel's memory saving merits optimization.
  If yes, profile and pipeline the tensor-core kernel before adding
  features; if no, keep ComfyUI and close the spike with the negative
  performance result rather than porting more models.
- Independently of Mojo, finish full-DiT correctness before attempting
  an end-to-end MAX Krea benchmark.
