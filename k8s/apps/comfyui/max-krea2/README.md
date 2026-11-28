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
```

`run.sh` exposes the venv's pinned cuBLAS to MAX. It does not borrow
libraries from ComfyUI. `requirements.in` records direct requirements;
`requirements.lock` is the full `uv pip freeze` of the tested environment.

## Micro-benchmark

One real Krea MLP projection, `M=4352, K=6144, N=16384` (the 1024² token
count), eight timed runs per process:

```sh
./run.sh micro_fp8.py bf16   # MAX cast+scale, then cuBLAS
./run.sh micro_fp8.py mojo   # custom Mojo dequant, then cuBLAS
./run.sh micro_fp8.py fused  # custom Mojo scalar tiled FP8-weight GEMM
./run.sh micro_fp8.py tc     # custom Mojo BF16 tensor-core tiled GEMM
./run.sh micro_fp8.py fp8    # MAX's native dynamic-FP8 Ideogram layer
```

Measured on the desktop RTX 5070 Ti with MAX 26.7.0 / Mojo 1.2.0. Times are
approximate medians; allocation is MAX's reported before/after device-memory
delta for the compiled test, not just the raw weight file.

| Path | Time | Allocation delta | Result |
|---|---:|---:|---|
| MAX dequant + cuBLAS (`bf16`) | 10.0 ms | 512 MiB | reference |
| Mojo dequant + cuBLAS (`mojo`) | 9.9 ms | 512 MiB | bit-identical checked slice |
| Mojo scalar fused GEMM (`fused`) | 953 ms | 256 MiB | relative L2 0.00022 on checked slice |
| Mojo tensor-core fused GEMM (`tc`) | 112 ms | 256 MiB | small full-tile relative L2 0.00169 |
| MAX native dynamic FP8 (`fp8`) | 2.03 s | 256 MiB | cosine 0.99962 on checked slice |

The custom elementwise operation is not a useful optimization: the graph
already performs it well and both dequant+cuBLAS paths materialize BF16
weights. The fused kernels keep the full weight packed and halve the measured
allocation delta. Tensor cores are about 8.5× faster than the scalar fused
kernel, but still about 11× slower than cuBLAS. The next performance work is
pipelining/register tiling and removing synchronous tile-copy overhead, not
rewriting more model code in Mojo.

Current fused-kernel limitations:

- GPU only; no tail handling. Scalar `fused` needs M/N/K divisible by 32;
  `tc` needs M/N divisible by 64 and K divisible by 32.
- Scalar accumulation or straightforward BF16 MMA, not an optimized FP8
  activation/weight tensor-core pipeline.
- No automatic offload and no complete sampler/text-encoder/VAE pipeline.

## DiT parity

Generate a reference with ComfyUI's venv and compare two blocks in MAX:

```sh
~/disks/1TB-2024/ai/venv/bin/python ref_dump.py \
  --layers 2 --txt 64 --out /tmp/krea2-ref-2-txt64.npz
./run.sh parity.py --layers 2 --quant fp8_scaled \
  --ref /tmp/krea2-ref-2-txt64.npz
# The tiling-compatible text length also permits:
./run.sh parity.py --layers 2 --quant fp8_mojo_tc \
  --ref /tmp/krea2-ref-2-txt64.npz
```

On the shared `txt64` reference, built-in MAX and `fp8_mojo_tc` both
report cosine 0.999752 and relative L2 0.0226 against ComfyUI's FP32
output. Cached two-block execution measured 14 ms for built-in MAX and
112 ms for the tensor-core Mojo path. Thus the custom kernel adds no
observed numerical error in this test, but is not yet a speed win.

`parity.py` prints numerical metrics but is not yet a full-model or
end-to-end correctness gate. A full 28-block forward pass and fixed-seed
multi-step image comparison remain open.
