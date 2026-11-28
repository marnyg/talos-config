# MiniMax H3 Image DiT on MAX/Mojo — spike

Spike `talos-config-vnb4`; follow-ups `l5km` and `qobn`. This is a **DiT-only
port** of ComfyUI's `comfy/ldm/minimax/model.py`, not a text-to-image pipeline.
It uses fixed Qwen-like context and latent inputs; Qwen3-VL, VAE, video, LoRAs
and ControlNet remain out of scope.

Implemented in `h3.py`:

- the two-block text-token refiner and all 50 main DiT blocks;
- packed `[text | reference images | target image]` host layout, with a tested
  reference-image case;
- partial split-half 3D RoPE, per-token AdaLN modality/timestep rows, and the
  precomputed `[1025, 8]` AdaLN curve;
- FP32 input/output projection islands and the six-step PDD schedule outside
  the graph, including runtime head selection/blending;
- BF16-dequantized weights or canonical packed W4A8 weights with custom Mojo
  Hadamard, activation quantization and signed-INT8 tensor-core kernels.

MAX 26.7's AdaLN GEMV fails at `M=1, K=8, N=96768`, so `CurveLinear` uses eight
explicit FP32 multiply/add terms. MAX also lacks NVIDIA signed-INT8 MMA and its
typed `TensorCore` operand loaders reject INT8. `kernels/h3_w4a8.mojo` therefore
loads m16n8k32 fragments directly and emits
`mma.sync.aligned.m16n8k32.row.col.s32.s8.s8.s32` through `inlined_assembly`.
Packed weights are decoded directly into B registers; there is no full decoded
weight workspace. A scaled epilogue applies row/channel scales in FP32 and
writes BF16 instead of materializing global INT32 projection outputs.

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

The two-block BF16 export is about 3 GB because it includes two main blocks
**and** the BF16 text refiner. The reference `.npz` contains both Comfy's
BF16-dequantized output and its actual W4A8 output, plus block intermediates.
`parity.py` checks that NumPy packed positions exactly equal Comfy's
`PackedLayout` before compiling. First compilation takes about one minute;
cached compilation is much faster.

Two main blocks, 64 text tokens + 256 image tokens, RTX 5070 Ti:

| Path | Warm forward | vs Comfy BF16, cosine / rel-L2 | vs Comfy W4A8, cosine / rel-L2 |
|---|---:|---:|---:|
| MAX BF16-dequantized | 9 ms | 0.99997985 / 0.00640 | 0.99997151 / 0.00760 |
| MAX packed W4A8 + fused Mojo | 15 ms | 0.99997294 / 0.00739 | 0.99996823 / 0.00799 |

Fusing weight decode into the MMA reduced setup allocation by about 0.8 GiB
relative to the earlier workspace implementation.

### Reference-image parity

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_dump.py \
  --layers 2 --latent 32 --txt 64 --ref-latent 16 \
  --reference-mode w4a8 --export-mode unquantized \
  --out /tmp/h3-ref-2-image.npz --weights /tmp/h3-unquantized-2.safetensors
./run.sh parity.py --layers 2 --quant w4a8 \
  --ckpt ~/disks/1TB-2024/ai/models/diffusion_models/minimax_h3_image_pruned_w4a8.safetensors \
  --weights /tmp/h3-unquantized-2.safetensors --ref /tmp/h3-ref-2-image.npz
```

This 384-token case (64 text + 64 reference + 256 target) uses two AdaLN time
levels and reports cosine **0.99996841** / relative L2 **0.00799** against
Comfy's actual W4A8 path.

## One-projection W4A8 gate

Generate the real `blocks.0.attn.out_proj` reference (`M=64, K=7168, N=5376`)
and benchmark Comfy, then validate each Mojo stage independently:

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_w4a8.py
for stage in dequant rotate quant mma mma-fused full; do
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
| fused packed decode → B registers → INT32 MMA | **bit-exact**, every accumulator |
| complete MAX/Mojo projection vs actual Comfy W4A8 | cosine **0.99999672**, rel-L2 **0.00369** |
| MAX/Mojo fused projection, synchronized | **0.27 ms** |
| Comfy W4A8, CUDA events | **0.183 ms** median |
| Comfy pre-dequantized BF16 GEMM, CUDA events | **0.107 ms** median |

The first fused implementation wrote a decoded shared-memory tile and took
0.667 ms for the MMA. Decoding directly into B registers cut that to 0.26 ms;
the earlier full-workspace pipeline took 0.80 ms end to end.

Activation quantization must reproduce two non-obvious Comfy details: the FP32
row scale is cast to BF16 before division, and the quotient is also cast to
BF16 before ties-to-even INT8 rounding. Weights clamp to `[-127, 127]` while
activations use `[-128, 127]`.

The full projection is not bit-exact because Comfy fuses rotation and
quantization without the separately materialized BF16 rotated tensor. An FP32
rotated-workspace variant was also measured and was slightly worse, so BF16 is
the default. `full-fp32` remains available as an exploratory comparison.

## Full 50-block DiT and PDD schedule

Generate the actual-W4A8 reference without creating a full BF16 checkpoint:

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_dump.py \
  --layers 50 --latent 32 --txt 64 --reference-mode w4a8 \
  --export-mode unquantized --debug-blocks 2 --schedule \
  --out /tmp/h3-ref-50.npz --weights /tmp/h3-unquantized-50.safetensors
./run.sh parity.py --layers 50 --quant w4a8 \
  --ckpt ~/disks/1TB-2024/ai/models/diffusion_models/minimax_h3_image_pruned_w4a8.safetensors \
  --weights /tmp/h3-unquantized-50.safetensors \
  --ref /tmp/h3-ref-50.npz --runs 4 --schedule
```

At 64 text + 256 image tokens:

- MAX warm forward: **0.316–0.325 s** after the first run; compilation is about
  108 s cold and 5 s cached.
- Comfy W4A8 median: **102.5 ms** over ten CUDA-event runs, so MAX is about
  **3.1× slower** at this small shape.
- MAX setup uses about **11.76 GiB** of its 12.75 GiB pool, leaving **768 MiB**.
  Comfy reports **13.12 GiB** peak allocated.
- Full forward cosine is **0.99363536** / relative L2 **0.112726**. This is a
  numerical comparison, not production-equivalent output.

Six-step PDD parity:

| Step | Sigma | Cosine | Relative L2 |
|---:|---:|---:|---:|
| 0 | 1.000000 | 0.99827290 | 0.05876 |
| 1 | 0.967524 | 0.99285185 | 0.11970 |
| 2 | 0.887549 | 0.99718136 | 0.07517 |
| 3 | 0.750000 | 0.99363536 | 0.11273 |
| 4 | 0.532774 | 0.99906951 | 0.04314 |
| 5 | 0.232009 | 0.99940270 | 0.03470 |

Execution results must be released before the next call; otherwise the next
forward can request a second workspace and OOM even at this small shape.
`parity.py` does that explicitly.

For the matching Comfy benchmark, add `--benchmark 10` to the 50-block
`ref_dump.py` command.

## 2048²-equivalent memory result

```sh
./run.sh parity.py --layers 50 --quant w4a8 \
  --ckpt ~/disks/1TB-2024/ai/models/diffusion_models/minimax_h3_image_pruned_w4a8.safetensors \
  --weights /tmp/h3-unquantized-50.safetensors \
  --latent 128 --txt 256 --runs 3
```

This is 4096 image tokens + 256 text tokens. The graph compiles (about 125 s),
but the first forward requests **18.76 GB for one allocation** and OOMs. There
is no full-resolution speed result. A quiet desktop alone cannot fix this;
2048² needs graph/activation-memory redesign or a different execution/offload
strategy.

## Remaining limitations

- Current kernels require M/N divisible by 64 and K divisible by 256. There is
  no tail handling; tested 320-, 384- and 4352-token sequences satisfy it.
- Full-resolution generation is not viable with the current graph. The small
  full-model path is correct enough for a spike but remains ~3.1× slower than
  Comfy and shows accumulated numerical drift.
- No Qwen3-VL text encoder, VAE, iterative image sampler, video, ref2va, LoRA,
  ControlNet or weight offload. ComfyUI remains the complete-pipeline tool.
