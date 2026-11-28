#!/usr/bin/env python3
"""Validate each Mojo W4A8 stage against ref_w4a8.py, then the full projection."""
import argparse
import os
import time
from pathlib import Path

import numpy as np
from max.driver import Accelerator
from max.dtype import DType
from max.experimental import functional as F
from max.experimental.nn import Module
from max.experimental.tensor import Tensor, TensorType
from max.graph.weights import load_weights

KERNELS = Path(__file__).parent / "kernels"
BF16 = DType.bfloat16
FP32 = DType.float32

ap = argparse.ArgumentParser()
ap.add_argument("--stage", choices=("dequant", "rotate", "quant", "mma", "mma-fused", "full", "full-fp32"), required=True)
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/minimax_h3_image_pruned_w4a8.safetensors"))
ap.add_argument("--ref", default="/tmp/h3-w4a8-ref.npz")
ap.add_argument("--runs", type=int, default=3)
a = ap.parse_args()
ref = np.load(a.ref)
layer = str(ref["layer"])
weights = load_weights([Path(a.ckpt)])
prefix = layer + "."
weight = weights[prefix + "weight"].data()
scales = weights[prefix + "weight_s_rel"].data()
codebook = weights[prefix + "weight_codebook"].data()
s_channel = weights[prefix + "weight_s_channel"].data()
M, K = ref["x"].shape
N = int(weight.shape[0])
dev = Accelerator()


def custom(name, values, types):
    return F.custom(name, device=dev, values=values, out_types=types)


def rotate(x):
    return custom("h3_convrot256", [x], [TensorType(BF16, [M, K], dev)])[0]


def quant(x):
    return custom(
        "h3_int8_quant", [x],
        [TensorType(DType.int8, [M, K], dev), TensorType(FP32, [M], dev)],
    )


def rotate_fp32(x):
    return custom("h3_convrot256_fp32", [x], [TensorType(FP32, [M, K], dev)])[0]


def quant_fp32(x):
    return custom(
        "h3_int8_quant_fp32", [x],
        [TensorType(DType.int8, [M, K], dev), TensorType(FP32, [M], dev)],
    )


def dequant(w, s, cb):
    return custom("h3_w4a8_dequant", [w, s, cb], [TensorType(DType.int8, [N, K], dev)])[0]


def mma(x, w):
    return custom("h3_int8_mma", [x, w], [TensorType(DType.int32, [M, N], dev)])[0]


def fused_mma(x, w, s, cb):
    return custom(
        "h3_w4a8_mma", [x, w, s, cb],
        [TensorType(DType.int32, [M, N], dev)],
    )[0]


def fused_linear(x, xs, w, s, cb, channel):
    return custom(
        "h3_w4a8_linear", [x, xs, w, s, cb, channel],
        [TensorType(BF16, [M, N], dev)],
    )[0]


class Projection(Module[..., Tensor]):
    def forward(self, *args):
        if a.stage == "rotate":
            return rotate(args[0].cast(BF16)).cast(FP32)
        if a.stage == "quant":
            return quant(args[0].cast(BF16))[0]
        if a.stage == "dequant":
            return dequant(*args)
        if a.stage == "mma":
            return mma(*args)
        if a.stage == "mma-fused":
            return fused_mma(*args)
        x, w, s, cb, channel = args
        if a.stage == "full-fp32":
            rotated = rotate_fp32(x.cast(BF16))
            xq, xs = quant_fp32(rotated)
        else:
            rotated = rotate(x.cast(BF16))
            xq, xs = quant(rotated)
        return fused_linear(xq, xs, w, s, cb, channel).cast(FP32)


if a.stage in ("rotate", "quant"):
    input_arrays = (ref["x"] if a.stage == "rotate" else ref["rotated"],)
    input_types = (TensorType(FP32, [M, K], dev),)
elif a.stage == "dequant":
    input_arrays = (weight, scales, codebook)
    input_types = (
        TensorType(DType.int8, weight.shape, dev),
        TensorType(DType.float8_e4m3fn, scales.shape, dev),
        TensorType(FP32, [16], dev),
    )
elif a.stage == "mma":
    input_arrays = (ref["xq"], ref["wq"])
    input_types = (
        TensorType(DType.int8, [M, K], dev),
        TensorType(DType.int8, [N, K], dev),
    )
elif a.stage == "mma-fused":
    input_arrays = (ref["xq"], weight, scales, codebook)
    input_types = (
        TensorType(DType.int8, [M, K], dev),
        TensorType(DType.int8, weight.shape, dev),
        TensorType(DType.float8_e4m3fn, scales.shape, dev),
        TensorType(FP32, [16], dev),
    )
else:
    input_arrays = (ref["x"], weight, scales, codebook, s_channel)
    input_types = (
        TensorType(FP32, [M, K], dev),
        TensorType(DType.int8, weight.shape, dev),
        TensorType(DType.float8_e4m3fn, scales.shape, dev),
        TensorType(FP32, [16], dev),
        TensorType(FP32, [N], dev),
    )


def gpu(value):
    if isinstance(value, np.ndarray):
        value = np.ascontiguousarray(value)
    return Tensor.from_dlpack(value).to(dev)


inputs = tuple(gpu(value) for value in input_arrays)
t0 = time.monotonic()
with F.lazy():
    model = Projection()
run_model = model.compile(*input_types, custom_extensions=[KERNELS])
print("compile", time.monotonic() - t0, dev.stats)
for i in range(a.runs):
    t0 = time.monotonic()
    result = run_model(*inputs)
    dev.synchronize()  # run() is asynchronous; exclude only the final D2H copy
    print(f"run {i}: {time.monotonic() - t0:.6f}s")
got = result.to_numpy()

expected = {
    "dequant": ref["wq"], "rotate": ref["rotated"], "quant": ref["xq_unfused"],
    "mma": ref["acc"], "mma-fused": ref["acc"],
    "full": ref["out"], "full-fp32": ref["out"],
}[a.stage]
print("stage", a.stage, "layer", layer, "shape", got.shape)
if got.dtype in (np.int8, np.int32):
    different = got != expected
    print("exact", not different.any(), "different", int(different.sum()),
          "max|d|", np.abs(got.astype(np.int64) - expected.astype(np.int64)).max())
    np.testing.assert_array_equal(got, expected)
else:
    cosine = float((got * expected).sum() / (np.linalg.norm(got) * np.linalg.norm(expected)))
    rel_l2 = np.linalg.norm(got - expected) / np.linalg.norm(expected)
    print("cosine", cosine, "rel-L2", rel_l2,
          "max|d|", np.abs(got - expected).max())
    if a.stage.startswith("full"):
        assert cosine > 0.9999 and rel_l2 < 0.02
    else:
        np.testing.assert_allclose(got, expected, rtol=2e-2, atol=2e-2)
print("PASS", a.stage)
