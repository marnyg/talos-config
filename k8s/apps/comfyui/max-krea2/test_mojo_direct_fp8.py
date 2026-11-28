#!/usr/bin/env python3
"""Compare direct Mojo FP8 MMA with MAX's native dynamic-FP8 linear."""
import argparse
import os
from pathlib import Path

import numpy as np
from max.driver import Accelerator
from max.dtype import DType
from max.experimental import functional as F
from max.experimental.nn import Module
from max.experimental.tensor import Tensor, TensorType
from max.graph.weights import load_weights
from max.pipelines.architectures.ideogram4.layers.fp8_linear import (
    fp8_matmul_2d,
    fp8_quantize_2d,
)

import krea2

ap = argparse.ArgumentParser()
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors"))
a = ap.parse_args()

weights = load_weights([Path(a.ckpt)])
weight = weights["blocks.0.mlp.up.weight"].data()
scale = weights["blocks.0.mlp.up.weight_scale"].data()
M = N = 64
K = 32


class Direct(Module[[Tensor, Tensor, Tensor], Tensor]):
    def forward(self, x: Tensor, w: Tensor, s: Tensor) -> Tensor:
        w = w[:N, :K]
        xq, xs = krea2._quantize_fp8(
            x.cast(DType.bfloat16), krea2._INPUT_SCALE_SPEC,
            krea2._WEIGHT_SCALE_SPEC, scales_type=DType.float32,
            out_type=DType.float8_e4m3fn,
        )
        y = F.custom(
            "krea2_fp8_mma", device=x.device, values=[xq, w],
            out_types=[TensorType(DType.float32, [M, N], x.device)],
        )[0]
        return (y * F.reshape(xs, [M, 1]) * s).cast(DType.float32)


class QuantizedBF16(Module[[Tensor, Tensor, Tensor], Tensor]):
    """Independent reference: quantized inputs, ordinary BF16 GEMM."""

    def forward(self, x: Tensor, w: Tensor, s: Tensor) -> Tensor:
        w = w[:N, :K]
        xq, xs = fp8_quantize_2d(x.cast(DType.bfloat16))
        y = xq.cast(DType.bfloat16) @ w.cast(DType.bfloat16).T
        return (y * F.reshape(xs, [M, 1]) * s).cast(DType.float32)


class Native(Module[[Tensor, Tensor, Tensor], Tensor]):
    def forward(self, x: Tensor, w: Tensor, s: Tensor) -> Tensor:
        w = w[:N, :K]
        ws = F.concat([F.reshape(s, [1, 1]) for _ in range(N)], axis=0)
        xq, xs = fp8_quantize_2d(x.cast(DType.bfloat16))
        return fp8_matmul_2d(xq, xs, w, ws).cast(DType.float32)


dev = Accelerator()
input_types = (
    TensorType(DType.float32, [M, K], dev),
    TensorType(DType.float8_e4m3fn, weight.shape, dev),
    TensorType(DType.float32, [], dev),
)
rng = np.random.default_rng(0)
x_input = Tensor.from_dlpack(rng.standard_normal((M, K), dtype=np.float32)).to(dev)
w_input = Tensor.from_dlpack(weight).to(dev)
s_input = Tensor.from_dlpack(scale).to(dev)
outputs = []
for model_cls in (QuantizedBF16, Native, Direct):
    with F.lazy():
        model = model_cls()
    run = model.compile(
        *input_types,
        custom_extensions=[krea2.KERNELS] if model_cls is Direct else (),
    )
    outputs.append(run(x_input, w_input, s_input).to_numpy())

ref, native, got = outputs
for name, candidate in (("native", native), ("direct Mojo", got)):
    rel_l2 = np.linalg.norm(candidate - ref) / np.linalg.norm(ref)
    cosine = float((candidate * ref).sum() / (np.linalg.norm(candidate) * np.linalg.norm(ref)))
    print(f"{name}: max|d|={np.abs(candidate - ref).max():.6g}; "
          f"rel-L2={rel_l2:.6g}; cosine={cosine:.8f}")
np.testing.assert_allclose(got, ref, rtol=2e-2, atol=2e-3)
print(f"PASS: direct Mojo FP8 MMA on {M}x{K}x{N} real FP8 weights")
