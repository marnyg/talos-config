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

A synthetic full-shape benchmark is available without a reference:

```sh
./run.sh parity.py --layers 28 --quant fp8_mojo_tc --runs 3
# Smaller probe: --latent 32 --txt 64 --save /tmp/full28.npy
```

**No full 28-block timing has been obtained.** With other desktop processes
present, MAX saw about 11 GiB available and model initialization OOMed on a
final 96 MiB allocation even at the small shape. The FP8 checkpoint alone
is 13.1 GB decimal. A clean/headless desktop or explicit weight streaming
is required before comparing a full MAX step with ComfyUI's measured
**0.95 s/it fp8_scaled** and **0.64 s/it int8_convrot** results. Do not
interpret the projection or two-block timings as end-to-end timings.
