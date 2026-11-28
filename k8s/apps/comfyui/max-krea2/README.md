# Krea 2 on MAX/Mojo — spike

Spike `talos-config-j1kd`. This is a partial Krea 2 DiT port, not a complete
text-to-image pipeline. `krea2.py` follows ComfyUI v0.45.5 and supports its
`fp8_scaled` Krea checkpoint. The default path uses MAX's Python graph API;
the optional `fp8_mojo*` paths call custom Mojo kernels in `kernels/`.

## Reproduce

Requirements: `uv`, an NVIDIA GPU supported by MAX 26.7, and the Krea
`fp8_scaled` checkpoint under `~/disks/1TB-2024/ai/models` (override with
`--ckpt` in the correctness tests). MAX and ComfyUI use separate venvs.

```sh
./setup.sh                 # MAX_VENV=/elsewhere is optional
./run.sh test_mojo_fp8.py --device gpu
./run.sh test_mojo_fused_fp8.py
./run.sh test_mojo_fused_fp8.py --tc
./run.sh test_mojo_direct_fp8.py
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

## Full 28-block DiT at 1024²

```sh
./run.sh parity.py --layers 28 --quant fp8_mojo_tc \
    --latent 128 --txt 256 --runs 3 --save /tmp/krea2-full28.npy
```

Measured on the RTX 5070 Ti, warm, one denoising step (the DiT forward):

| | per step | 8-step image |
|---|---:|---:|
| **MAX `fp8_mojo_tc`, 28 blocks, 1024²** | **6.41 s** | ~51 s |
| ComfyUI Krea 2 `fp8_scaled`, 1024², 8 steps | 0.95 s/it | 8.8 s |
| ComfyUI Krea 2 `int8_convrot`, 1024², 8 steps | 0.64 s/it | 6.2 s |

So the MAX port runs the full model but is **~6.7× slower per step** than
ComfyUI's `fp8_scaled` path, and ~10× slower than `int8_convrot`. That is
the honest end-to-end result: the port is correct and complete for the DiT,
but neither the kernel nor the runtime wins on this card.

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

### Correctness caveat

There is still **no 28-block / 1024² numerical reference** — `ref_dump.py`
runs ComfyUI's model in FP32 on the CPU, which at 28 blocks × 4096 tokens
is hours. Correctness rests on the two-block parity (cosine 0.999753 vs
ComfyUI FP32) plus the bit-exact per-op tests. Treat the full-model output
as structurally validated, not bit-validated.
