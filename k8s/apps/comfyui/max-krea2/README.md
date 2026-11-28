# Krea 2 on MAX/Mojo — spike

Spike `talos-config-j1kd`. This is a partial Krea 2 DiT port, not a complete
text-to-image pipeline. `krea2.py` follows ComfyUI v0.45.5 and supports its
`fp8_scaled` and mixed `nvfp4` Krea checkpoints. The default path uses MAX's
Python graph API; the optional `fp8_mojo*` paths and NVFP4 W4A16 path call
custom Mojo kernels in `kernels/`.

## Reproduce

Requirements: `uv`, an NVIDIA GPU supported by MAX 26.7, and the Krea
checkpoint under `~/disks/1TB-2024/ai/models` (override with `--ckpt` in the
correctness tests). MAX and ComfyUI use separate venvs.

```sh
./setup.sh                 # MAX_VENV=/elsewhere is optional
./run.sh test_mojo_fp8.py --device gpu
./run.sh test_mojo_fused_fp8.py
./run.sh test_mojo_fused_fp8.py --tc
./run.sh test_mojo_direct_fp8.py
./run.sh test_mojo_nvfp4.py
./run.sh test_mojo_nvfp4.py --row 1024 --k-offset 1024
```

`run.sh` exposes the venv's pinned cuBLAS to MAX. It does not borrow
libraries from ComfyUI. `requirements.in` records direct requirements;
`requirements.lock` is the full `uv pip freeze` of the tested environment.

## Projection benchmark and ComfyUI comparison

One real Krea MLP projection, `M=4352, K=6144, N=16384` (4096 image tokens
plus 256 text tokens), eight timed runs per process:

```sh
./run.sh micro_fp8.py bf16   # bound FP8 weight, MAX dequant + cuBLAS
./run.sh micro_fp8.py input  # runtime FP8 weight input + cuBLAS
./run.sh micro_fp8.py mojo   # custom Mojo dequant + cuBLAS
./run.sh micro_fp8.py fused  # custom Mojo scalar tiled GEMM
./run.sh micro_fp8.py tc     # custom Mojo BF16 tensor-core tiled GEMM
./run.sh micro_fp8.py direct # dynamic activation quant + direct FP8 MMA
./run.sh micro_fp8.py fp8    # MAX native dynamic-FP8 Ideogram layer
./run.sh profile_direct.py   # split quantization from direct FP8 MMA
~/disks/1TB-2024/ai/venv/bin/python micro_comfy.py  # ComfyUI comparison
```

Measured on the desktop RTX 5070 Ti with MAX 26.7.0 / Mojo 1.2.0. Times are
approximate medians. Allocation is MAX's before/after device-memory delta
for the compiled test, not just the raw weight file. The ComfyUI/PyTorch
row was measured separately in the ComfyUI venv with the same BF16 GEMM.

| Path | Time | Allocation delta |
|---|---:|---:|
| **ComfyUI/PyTorch BF16 GEMM** | **9.2–9.6 ms** | not measured |
| MAX dequant + cuBLAS (`bf16`) | 10.0 ms | 512 MiB |
| MAX runtime-weight input + cuBLAS (`input`) | 11–14 ms | **256 MiB** |
| Mojo dequant + cuBLAS (`mojo`) | 9.9 ms | 512 MiB |
| Mojo tensor-core fused GEMM (`tc`) | **36–38 ms** | **256 MiB** |
| Mojo scalar fused GEMM (`fused`) | 953 ms | 256 MiB |
| Mojo direct FP8 MMA (`direct`) | 1.08 s | 256 MiB |
| MAX native dynamic FP8 (`fp8`) | 2.03 s | 256 MiB |

The optimization pass reduced `tc` from 112 ms to 36–38 ms by transposing B
during tensor-core fragment loads, using every block thread for tile copies,
and choosing a 64x32 warp tile. Larger/smaller blocks and K=64 were slower.
It remains about 4x slower than ComfyUI/PyTorch on this projection.
`micro_comfy.py` runs the ComfyUI side and shares the exact NumPy input;
the optimized Mojo and ComfyUI output slices are bit-for-bit identical.

The custom elementwise operation is not a useful optimization: output and
speed are effectively unchanged and both dequant paths materialize BF16
weights. Runtime-weight inputs avoid that expansion in the microbenchmark,
but the full model currently binds its weights at compile time.

### NVFP4 projection

`krea2_turbo_nvfp4` mixes W4A4 (QKV and MLP gate/up) with W4A16 (attention
gate/output and MLP down), as marked by its `full_precision_matrix_mult`
metadata. Run the same real MLP projection through both forms:

```sh
./run.sh micro_nvfp4.py native  # MAX native W4A4
./run.sh micro_nvfp4.py mojo    # custom fused W4A16
./run.sh micro_nvfp4.py dequant # custom dequant + cuBLAS W4A16
~/disks/1TB-2024/ai/venv/bin/python micro_comfy_nvfp4.py
```

| Path | Time | Allocation delta |
|---|---:|---:|
| ComfyUI NVFP4 W4A4 quant + GEMM | **1.5 ms** | not measured |
| MAX native NVFP4 W4A4 | **2.6 ms** | 256 MiB |
| ComfyUI W4A16 BF16 GEMM | 9–10 ms | not measured |
| MAX NVFP4 dequant + cuBLAS W4A16 | 9.6–11.1 ms | 256 MiB |
| Mojo fused NVFP4 W4A16 | 62 ms | 256 MiB |

Both MAX W4A16 outputs are bit-identical to ComfyUI. MAX native W4A4 matches
ComfyUI's unit-global-scale result at cosine 0.999996. The checkpoint has no
static input scales, so ComfyUI normally recomputes a global activation scale;
MAX 26.7 requires that scale as a host scalar and cannot consume a computed
device scalar in this graph API. The port therefore uses unit global scale,
which changes FP8 scale and FP4 value rounding. Full-model parity below
quantifies the effect instead of hiding it.

The dequant+cuBLAS row is not used in the full model: MAX materializes every
bound BF16 weight during setup and OOMs. The mixed full model uses native W4A4
and the packed fused W4A16 kernel. `nvfp4_mojo_tc` uses fused W4A16 everywhere
for isolation and debugging.

The fused kernels keep the full weight packed. Direct FP8 MMA does not help:
`profile_direct.py` puts activation quantization at ~0.2 ms and the MMA plus
scaling at ~1.1 s. Its correctness test uses an independent quantized-BF16
reference (cosine 0.99999845); MAX's native FP8 path is not the oracle.

Current limitations:

- GPU only; no tail handling. Scalar `fused` needs M/N/K divisible by 32;
  `tc` needs M/N divisible by 64 and K divisible by 32.
- Straightforward synchronous tile copies and BF16 MMA, not a pipelined
  kernel. No automatic offload or complete sampler/text-encoder/VAE pipeline.
- The full-model path needs the desktop's GPU footprint under ~1 GiB (see
  below); it is not a "just run it" configuration.

## DiT parity and full-model status

Generate a reference with ComfyUI's venv and compare two blocks in MAX:

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_dump.py \
  --layers 2 --txt 64 --out /tmp/krea2-ref-2-txt64.npz
./run.sh parity.py --layers 2 --quant fp8_scaled \
  --ref /tmp/krea2-ref-2-txt64.npz
./run.sh parity.py --layers 2 --quant fp8_mojo_tc \
  --ref /tmp/krea2-ref-2-txt64.npz
```

On the shared reference, built-in MAX and `fp8_mojo_tc` both report cosine
0.999752 and relative L2 0.0226 against ComfyUI's FP32 output. Cached
execution measured 14 ms for built-in MAX and 49 ms for optimized Mojo,
down from 112 ms before the optimization pass.

For NVFP4, `ref_dump_nvfp4.py` runs the actual quantized ComfyUI model on the
GPU (not an FP32 CPU approximation) and preserves its quantization metadata:

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_dump_nvfp4.py \
  --layers 2 --txt 64 --out /tmp/krea2-nvfp4-ref-2.npz
./run.sh parity.py --layers 2 --quant nvfp4 \
  --ref /tmp/krea2-nvfp4-ref-2.npz
```

Two-block mixed NVFP4 parity is cosine 0.999766 / relative L2 0.0222 against
ComfyUI's normal dynamic-scale output. The all-W4A16 isolation path reports
0.999972 against a ComfyUI `--w4a16` reference.

## Full 28-block DiT at 1024²

```sh
./run.sh parity.py --layers 28 --quant fp8_mojo_tc \
    --latent 128 --txt 256 --runs 3 --save /tmp/krea2-full28.npy
```

Measured on the RTX 5070 Ti, warm, one denoising step (the DiT forward):

| | per step | 8-step image |
|---|---:|---:|
| MAX `fp8_mojo_tc`, 28 blocks, 1024² | 6.41 s | ~51 s |
| **MAX mixed `nvfp4`, 28 blocks, 1024²** | **3.21 s** | ~26 s |
| ComfyUI Krea 2 `fp8_scaled`, 1024², 8 steps | 0.95 s/it | 8.8 s |
| ComfyUI Krea 2 `nvfp4`, 1024², 8 steps | 0.90 s/it | 8.1 s |
| ComfyUI Krea 2 `int8_convrot`, 1024², 8 steps | 0.64 s/it | 6.2 s |

NVFP4 halves the MAX port's step time, but it remains ~3.6× slower than
ComfyUI's NVFP4 path. Unlike FP8, it fits with the desktop still running:
with ~1.7 GiB already in use, MAX starts with a 12.75 GiB pool and retains
5.0 GiB after model setup. No desktop GPU processes need to be stopped.

### Why it only just fits

MAX's device pool equals the free VRAM at `Accelerator` creation (verified:
a 2 GiB pre-allocation shrinks the pool 12.25 → 10.25 GiB). The FP8 weights
are **12.24 GiB and fully resident** — there is no weight offload in MAX 26.7
(`cpu_offload`/`weight_offload` do not exist; the only `offload` is the LLM
KV-cache tiering), and the VMM reports `reclaimable=0`, so nothing pages to
host. ComfyUI fits the same checkpoint by offloading layers; MAX cannot.

Consequently 1024² needs the desktop's GPU footprint under ~1 GiB:

```
desktop ≤ ~1.1 GiB  →  pool ≈ 13.5 GiB
weights                12.24 GiB
activations             1.25 GiB   (was 2.10 GiB before the two optimizations)
needed                ~13.49 GiB
```

The two footprint optimizations (BF16 RoPE, BF16 kernel output) took the
activation request from 2.24 GB to 1.34 GB, which is what made 1024²
reachable at all. Anything above 768² (`--latent 96`) failed before them.

### Full-model correctness

There is still no 28-block FP8 reference: `ref_dump.py` runs ComfyUI's model
in FP32 on the CPU, which at 28 blocks × 4096 tokens is hours. NVFP4 does
have a real full-model ComfyUI GPU reference:

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_dump_nvfp4.py \
  --layers 28 --latent 128 --txt 256 --out /tmp/krea2-nvfp4-ref-28.npz
./run.sh parity.py --layers 28 --quant nvfp4 \
  --ref /tmp/krea2-nvfp4-ref-28.npz --runs 3
```

At 1024², MAX mixed NVFP4 reports cosine **0.993576** / relative L2 0.1267
against ComfyUI's normal dynamic-scale reference. Against ComfyUI's matching
`--unit-scale` reference it reports **0.997914** / 0.0826. This is numerical
validation, not bit equality; the remaining difference includes the documented
global-scale restriction and accumulation/rounding differences across 28
blocks. The port is still DiT-only: no sampler, text encoder or VAE.
