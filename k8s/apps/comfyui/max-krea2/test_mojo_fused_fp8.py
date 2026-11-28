#!/usr/bin/env python3
"""Small GPU correctness test for the fused Mojo FP8-weight linear."""
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

import krea2

ap = argparse.ArgumentParser()
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors"))
ap.add_argument("--tc", action="store_true", help="test the tensor-core variant")
a = ap.parse_args()

weights = load_weights([Path(a.ckpt)])
weight = weights["blocks.0.mlp.up.weight"].data()
scale = weights["blocks.0.mlp.up.weight_scale"].data()
M = N = 64
K = 32


class Linear(Module[[Tensor, Tensor, Tensor], Tensor]):
    def __init__(self, fused: bool) -> None:
        self.fused = fused

    def forward(self, x: Tensor, w: Tensor, s: Tensor) -> Tensor:
        x = x.cast(DType.bfloat16)
        w = w[:N, :K]
        if self.fused:
            y = F.custom(
                "krea2_fp8_linear_tc" if a.tc else "krea2_fp8_linear",
                device=x.device,
                values=[x, w, F.reshape(s, [1])],
                out_types=[TensorType(DType.bfloat16, [M, N], x.device)],
            )[0]
        else:
            w = w.cast(DType.bfloat16) * s.cast(DType.bfloat16)
            y = x @ w.T
        return y.cast(DType.float32)


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
for fused in (False, True):
    with F.lazy():
        model = Linear(fused)
    run = model.compile(
        *input_types,
        custom_extensions=[krea2.KERNELS] if fused else (),
    )
    outputs.append(run(x_input, w_input, s_input).to_numpy())

ref, got = outputs
rel_l2 = np.linalg.norm(got - ref) / np.linalg.norm(ref)
cosine = float((got * ref).sum() / (np.linalg.norm(got) * np.linalg.norm(ref)))
np.testing.assert_allclose(got, ref, rtol=1e-2, atol=1e-3)
print(f"PASS: fused Mojo {'tensor-core ' if a.tc else ''}linear on {M}x{K}x{N} real FP8 weights")
print(f"max|d|={np.abs(got - ref).max():.6g}; rel-L2={rel_l2:.6g}; cosine={cosine:.8f}")
