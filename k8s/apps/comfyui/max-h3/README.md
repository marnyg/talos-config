# MiniMax H3 Image DiT on MAX/Mojo — spike

Spike `talos-config-vnb4`. This is a **DiT-only, two-block correctness port** of
ComfyUI's `comfy/ldm/minimax/model.py`, not a text-to-image pipeline. It uses
fixed Qwen-like context and latent inputs; Qwen3-VL, VAE, video, the sampler,
LoRAs and ControlNet remain out of scope.

Implemented in `h3.py`:

- the two-block text-token refiner and selectable number of main DiT blocks;
- packed `[text | reference images | target image]` host layout (the tested
  reference is text-to-image without reference rows);
- partial split-half 3D RoPE, per-token AdaLN modality/timestep rows, and the
  precomputed `[1025, 8]` AdaLN curve;
- FP32 input/output projection islands and runtime PDD head selection/blending;
- BF16-dequantized weights or canonical packed W4A8 weights with custom Mojo
  Hadamard, activation quantization, weight decode and signed-INT8 tensor-core
  MMA kernels.

MAX 26.7's AdaLN GEMV fails at `M=1, K=8, N=96768`, so `CurveLinear` uses eight
explicit FP32 multiply/add terms. MAX also lacks NVIDIA signed-INT8 MMA and its
typed `TensorCore` operand loaders reject INT8. `kernels/h3_w4a8.mojo` therefore
loads the m16n8k32 fragments directly and emits
`mma.sync.aligned.m16n8k32.row.col.s32.s8.s8.s32` through `inlined_assembly`.

## Reproduce

Requirements: `uv`, an NVIDIA GPU supported by MAX 26.7 (tested on the desktop
RTX 5070 Ti), ComfyUI's venv, and
`minimax_h3_image_pruned_w4a8.safetensors` under
`~/disks/1TB-2024/ai/models/diffusion_models`.

```sh
./setup.sh                 # MAX_VENV=/elsewhere is optional
~/disks/1TB-2024/ai/venv/bin/python ref_dump.py \
  --layers 2 --latent 32 --txt 64 \
  --out /tmp/h3-ref-2.npz --weights /tmp/h3-bf16-2.safetensors
./run.sh parity.py --layers 2 --quant bf16 \
  --weights /tmp/h3-bf16-2.safetensors --ref /tmp/h3-ref-2.npz
./run.sh parity.py --layers 2 --quant w4a8 \
  --ckpt ~/disks/1TB-2024/ai/models/diffusion_models/minimax_h3_image_pruned_w4a8.safetensors \
  --weights /tmp/h3-bf16-2.safetensors --ref /tmp/h3-ref-2.npz
```

The export is about 3 GB because it includes two main blocks **and** the
BF16 text refiner. The reference `.npz` contains both Comfy's BF16-dequantized
output and its actual W4A8 output, plus block intermediates. `parity.py` checks
that the NumPy packed positions exactly equal Comfy's `PackedLayout` before
compiling. First compilation takes about one minute; cached compilation is much
faster.

Two main blocks, 64 text tokens + 256 image tokens, RTX 5070 Ti:

| Path | Warm forward | vs Comfy BF16, cosine / rel-L2 | vs Comfy W4A8, cosine / rel-L2 |
|---|---:|---:|---:|
| MAX BF16-dequantized | 9 ms | 0.99997985 / 0.00640 | 0.99997151 / 0.00760 |
| MAX packed W4A8 + Mojo | 17–18 ms | 0.99997294 / 0.00739 | 0.99996823 / 0.00799 |

These are numerical parity results, not full-model or production benchmarks.
The W4A8 setup keeps weights packed but still allocates an INT8 decoded-weight
workspace for each projection.

## One-projection W4A8 gate

Generate the real `blocks.0.attn.out_proj` reference (`M=64, K=7168, N=5376`)
and benchmark Comfy, then validate each Mojo stage independently:

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_w4a8.py
for stage in dequant rotate quant mma full; do
  ./run.sh test_w4a8.py --stage "$stage" --runs 8 || exit
done
```

Results:

| Check | Result |
|---|---|
| packed codebook/group-scale → INT8 weight | **bit-exact**, all 38.5M values |
| 256-wide regular-H4 Hadamard vs unfused reference | **bit-exact** |
| row activation quantization vs equally unfused reference | **bit-exact** |
| signed INT8 × INT8 → INT32 MMA | **bit-exact**, every accumulator |
| complete MAX/Mojo projection vs actual Comfy W4A8 | cosine **0.99999672**, rel-L2 **0.00369** |
| MAX/Mojo projection, synchronized | **0.80 ms** |
| Comfy W4A8, CUDA events | **0.183 ms** median |
| Comfy pre-dequantized BF16 GEMM, CUDA events | **0.107 ms** median |

Activation quantization must reproduce two non-obvious Comfy details: the FP32
row scale is cast to BF16 before division, and the quotient is also cast to
BF16 before ties-to-even INT8 rounding. Weights clamp to `[-127, 127]` while
activations use `[-128, 127]`.

The full projection is not bit-exact because Comfy fuses rotation and
quantization without the separately materialized BF16 rotated tensor. An FP32
rotated-workspace variant was also measured and was slightly worse
(cosine 0.999963 before the quotient-rounding fix), so BF16 is the default.

## Remaining work / limitations

- **Performance gate not passed yet.** The one-projection custom path is about
  4.4× slower than Comfy W4A8. Fuse packed-weight decode into the MMA and remove
  the full INT8 weight workspace before benchmarking a full 50-block model.
- Current kernels require M/N divisible by 64 and K divisible by 256. There is
  no tail handling; the tested 320-token sequence satisfies the alignment.
- Only two main blocks have been run. There is no full 50-block result, sampler
  schedule, text encoder, VAE, video path, offload, ref2va, LoRA or ControlNet.
- Reference-image host packing is implemented but not yet covered by a Comfy
  reference run.
- `full-fp32` in `test_w4a8.py` is an exploratory comparison, not the default.
