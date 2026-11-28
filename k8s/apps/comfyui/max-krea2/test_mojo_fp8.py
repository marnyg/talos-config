#!/usr/bin/env python3
"""CPU correctness test for the custom Mojo FP8 dequantization op.

Uses a small slice of the real Krea 2 checkpoint and compares against the
original MAX graph expression. No GPU is used.
"""
import argparse
import os
from pathlib import Path

import numpy as np
from max.driver import Accelerator, CPU
from max.dtype import DType
from max.experimental import functional as F
from max.experimental.nn import Module
from max.experimental.tensor import Tensor, TensorType
from max.graph.weights import load_weights

import krea2

ap = argparse.ArgumentParser()
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors"))
ap.add_argument("--device", choices=("cpu", "gpu"), default="cpu")
a = ap.parse_args()

weights = load_weights([Path(a.ckpt)])
weight = weights["blocks.0.mlp.up.weight"].data()
scale = weights["blocks.0.mlp.up.weight_scale"].data()
# Whole rows keep the sliced weight contiguous; the custom-op API's arbitrary
# strided-view support is not what this test is intended to probe.
rows, cols = 8, 6144


class Dequant(Module[[Tensor, Tensor], Tensor]):
    def __init__(self, mojo: bool) -> None:
        self.mojo = mojo

    def forward(self, w: Tensor, s: Tensor) -> Tensor:
        w = w[:rows, :]
        if self.mojo:
            w = F.custom(
                "krea2_fp8_dequant",
                device=w.device,
                values=[w, F.reshape(s, [1])],
                out_types=[TensorType(DType.bfloat16, w.shape, w.device)],
            )[0]
        else:
            w = w.cast(DType.bfloat16) * s.cast(DType.bfloat16)
        return w.cast(DType.float32)


dev = CPU() if a.device == "cpu" else Accelerator()
input_types = (
    TensorType(DType.float8_e4m3fn, weight.shape, dev),
    TensorType(DType.float32, [], dev),
)
weight_input = Tensor.from_dlpack(weight).to(dev)
scale_input = Tensor.from_dlpack(scale).to(dev)
outputs = []
for mojo in (False, True):
    with F.lazy():
        model = Dequant(mojo)
    run = model.compile(
        *input_types,
        custom_extensions=[krea2.KERNELS] if mojo else (),
    )
    outputs.append(run(weight_input, scale_input).to_numpy())

np.testing.assert_array_equal(outputs[1], outputs[0])
print(f"PASS: Mojo matches MAX exactly on {rows}x{cols} real FP8 weights")
print(f"scale={float(np.from_dlpack(scale)):.8g}; output range "
      f"[{outputs[1].min():.6g}, {outputs[1].max():.6g}]")
