#!/usr/bin/env python3
"""Correctness test for fused NVFP4 W4A16 linear on real Krea weights."""
import argparse
import os
from pathlib import Path

import numpy as np
from max.driver import Accelerator, Buffer
from max.dtype import DType
from max.experimental import functional as F
from max.experimental.nn import Module
from max.experimental.tensor import Tensor, TensorType
from max.graph import Shape
from max.graph.weights import WeightData, load_weights

import krea2

CKPT = os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_nvfp4.safetensors"
)
PREFIX = "blocks.0.mlp.up"
ap = argparse.ArgumentParser()
ap.add_argument("--row", type=int, default=0)
ap.add_argument("--k-offset", type=int, default=0)
a = ap.parse_args()
M = 64
N = 128  # two output tiles: catches local/global scale-row indexing bugs
K = 32
ROW, K_OFFSET = a.row, a.k_offset


def bf16(x: np.ndarray) -> np.ndarray:
    """Round float32 to bfloat16 and return it as float32."""
    bits = np.ascontiguousarray(x, dtype=np.float32).view(np.uint32)
    rounded = (bits + np.uint32(0x7FFF) + ((bits >> 16) & 1)) & np.uint32(0xFFFF0000)
    return rounded.view(np.float32)


def fp8_e4m3(bits: np.ndarray) -> np.ndarray:
    bits = bits.astype(np.uint32)
    sign = np.where(bits & 0x80, np.float32(-1), np.float32(1))
    exponent = ((bits >> 3) & 0xF).astype(np.int32)
    mantissa = (bits & 7).astype(np.float32) / 8
    magnitude = np.where(
        exponent,
        (1 + mantissa) * np.exp2((exponent - 7).astype(np.float32)),
        mantissa * np.float32(2**-6),
    )
    return sign * magnitude


def original_reference(
    packed: np.ndarray, scales: np.ndarray, global_scale: float, x: np.ndarray
) -> np.ndarray:
    """Decode the original hi-first weights and swizzled scales independently."""
    rows, scale_cols = scales.shape
    codes = np.empty((N, K), dtype=np.uint8)
    tile = packed[ROW : ROW + N, K_OFFSET // 2 : (K_OFFSET + K) // 2]
    codes[:, 0::2] = tile >> 4
    codes[:, 1::2] = tile & 0xF
    lut = np.array(
        [0, 0.5, 1, 1.5, 2, 3, 4, 6, -0, -0.5, -1, -1.5, -2, -3, -4, -6],
        dtype=np.float32,
    )
    scale_bytes = scales.reshape(-1)
    plain_scales = np.empty((N, K // 16), dtype=np.uint8)
    for local_row in range(N):
        for local_block in range(K // 16):
            row, block = ROW + local_row, K_OFFSET // 16 + local_block
            i0, i1 = row // 128, block // 4
            i2, i3, i4 = row % 32, (row % 128) // 32, block % 4
            flat = ((((i0 * (scale_cols // 4) + i1) * 32 + i2) * 4 + i3) * 4 + i4)
            plain_scales[local_row, local_block] = scale_bytes[flat]
    total_scale = bf16(bf16(fp8_e4m3(plain_scales)) * bf16(global_scale))
    weight = bf16(lut[codes] * np.repeat(total_scale, 16, axis=1))
    return bf16(x) @ weight.T


class Wrap(Module[[Tensor], Tensor]):
    def __init__(self) -> None:
        self.lin = krea2.MojoTensorCoreNVFP4Linear(K, N)

    def forward(self, x: Tensor) -> Tensor:
        return self.lin(x).cast(DType.float32)


weights = load_weights([Path(CKPT)])
original_weight = weights[PREFIX + ".weight"].data().to_buffer().to_numpy()
original_scales = weights[PREFIX + ".weight_scale"].data().to_buffer().view(DType.uint8).to_numpy()
global_scale = float(np.from_dlpack(weights[PREFIX + ".weight_scale_2"].data()))

weight = krea2.prepare_nvfp4_weight(PREFIX + ".weight", weights[PREFIX + ".weight"].data())
scales = krea2.prepare_nvfp4_weight(PREFIX + ".weight_scale", weights[PREFIX + ".weight_scale"].data())
weight_np = np.ascontiguousarray(
    weight.to_buffer().to_numpy()[ROW : ROW + N, K_OFFSET // 2 : (K_OFFSET + K) // 2]
)
scales_np = np.ascontiguousarray(
    scales.to_buffer().view(DType.uint8).to_numpy()[ROW : ROW + N, K_OFFSET // 16 : (K_OFFSET + K) // 16]
)
scales_buf = Buffer.from_numpy(scales_np).view(DType.float8_e4m3fn, Shape(scales_np.shape))
sd = {
    "lin.weight": weight_np,
    "lin.weight_scale": scales_buf,
    "lin.weight_scale_2": np.array(global_scale, dtype=np.float32),
}

rng = np.random.default_rng(0)
x = rng.standard_normal((M, K), dtype=np.float32)
expected = original_reference(original_weight, original_scales, global_scale, x)

dev = Accelerator()
with F.lazy():
    model = Wrap()
    model.to(dev)
run = model.compile(
    TensorType(DType.float32, [M, K], dev),
    weights=sd,
    custom_extensions=[krea2.KERNELS],
)
got = run(Tensor.from_dlpack(x).to(dev)).to_numpy()
rel_l2 = np.linalg.norm(got - expected) / np.linalg.norm(expected)
cosine = float((got * expected).sum() / (np.linalg.norm(got) * np.linalg.norm(expected)))
np.testing.assert_allclose(got, expected, rtol=1e-2, atol=1e-3)
print(f"PASS: fused Mojo NVFP4 W4A16 linear on {M}x{K}x{N} real weights at row={ROW}, k={K_OFFSET}")
print(f"max|d|={np.abs(got - expected).max():.6g}; rel-L2={rel_l2:.6g}; cosine={cosine:.8f}")
