# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): H3 Image DiT + W4A8 on MAX/Mojo

- `vnb4` has a two-block H3 Image DiT port in
  `k8s/apps/comfyui/max-h3`: text refiner, packed layout, partial split-half
  RoPE, AdaLN curve/modality rows, FP32 islands and runtime PDD blending.
  With 64 text + 256 image tokens, BF16 runs in 9 ms (cosine 0.99997985
  against Comfy's BF16 reference); packed W4A8 runs in 17–18 ms (cosine
  0.99996823 against Comfy's actual W4A8).
- The custom W4A8 primitives are validated on a real `M=64, K=7168, N=5376`
  projection: packed decode, Hadamard, unfused activation quantization and
  direct-PTX signed-INT8 MMA are each bit-exact. The complete projection has
  cosine 0.99999672 and takes 0.80 ms; Comfy W4A8 takes 0.183 ms.
- README and reference generators contain reproduction commands and measured
  semantics. Notably, Comfy rounds both activation scale and quotient to BF16
  before INT8 rounding. MAX's typed INT8 TensorCore path is absent, so the
  kernel emits m16n8k32 signed-INT8 PTX directly.

## Same day, another session: the protocol network is live (`k7sd` line)

- ADR-0051 and protocol ADR-0012 Accepted. Fly app `marnyg-sap-net`
  (`actors/net/`) runs the lighthouse with the relay behind its
  admission gate; lighthouse `ed:0db45a…1615`. The sap plane moved onto
  it (`ssau`): provisioner + parent home there, `sap-lighthouse`
  retired (PV kept). First child born through the gate and settled
  (session `4d42d6b6`) after a child-side ordering fix (`aa57a27`,
  `67b2694`). Details: `technical/deployed-state.md`.
- Loose: `e7f0` (enforce the hub relay's member gate) needs a code
  switch — `relayAdmitter.gate` is never set in production — and a hub
  deploy, so the owner unseals afterwards. `w0ur` (relay/publish links
  do not feed the clock mark); ADR-0012's undecided four (signed id
  claim, stranger door, onward delegation, byte limit).

## Loose threads

- **Performance gate is not passed.** The custom projection is ~4.4× slower
  than Comfy and still materializes an INT8 decoded-weight workspace. `l5km`
  owns fusing decode into the MMA before a full-model performance claim.
- `qobn` owns the full 50-block DiT, sampler schedule, reference-image parity,
  and small-shape/2048² memory measurements. Current kernels require M/N
  divisible by 64 and K by 256 and have no tail handling.
- Still no Qwen3-VL text encoder, VAE, sampler, video, ref2va, LoRA, ControlNet
  or offload. ComfyUI remains the complete-pipeline tool; MAX is DiT-only.
- Krea 2 (`j1kd`) is answered and ready to close as previously recorded.
- Desktop ComfyUI is stopped and is not a service; restart with
  `k8s/apps/comfyui/run.sh` when needed.

## Suggested next steps

1. Do `l5km`: fuse packed decode into INT8 MMA, remove the workspace, then
   rerun `test_w4a8.py` and the Comfy projection benchmark.
2. Only after that measurement, do `qobn` and decide whether the full DiT is
   worth pursuing at 2048².
3. Add a reference-image Comfy dump before relying on the already-implemented
   host packing for editing.
