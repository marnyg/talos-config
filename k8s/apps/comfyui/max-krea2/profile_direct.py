#!/usr/bin/env python3
"""Split dynamic activation quantization from direct FP8 MMA timing."""
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

import krea2

CKPT = os.path.expanduser("~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors")
M, K, N = 4352, 6144, 16384


class Quantize(Module[[Tensor], tuple[Tensor, Tensor]]):
    def forward(self, x: Tensor) -> tuple[Tensor, Tensor]:
        return krea2._quantize_fp8(
            x.cast(DType.bfloat16), krea2._INPUT_SCALE_SPEC,
            krea2._WEIGHT_SCALE_SPEC, scales_type=DType.float32,
            out_type=DType.float8_e4m3fn,
        )


class MMA(Module[..., tuple[Tensor, Tensor]]):
    weight: Tensor
    weight_scale: Tensor

    def __init__(self) -> None:
        self.weight = Tensor.zeros([N, K], dtype=DType.float8_e4m3fn)
        self.weight_scale = Tensor.zeros([], dtype=DType.float32)

    def forward(self, xq: Tensor, xs: Tensor) -> tuple[Tensor, Tensor]:
        y = F.custom(
            "krea2_fp8_mma", device=xq.device, values=[xq, self.weight],
            out_types=[TensorType(DType.float32, [M, N], xq.device)],
        )[0]
        y = y * F.reshape(xs, [M, 1]) * self.weight_scale
        return y[:64, :64], F.mean(y)


def timings(run, args, sync_index=-1, runs=6):
    result = []
    for _ in range(runs):
        t0 = time.monotonic()
        out = run(*args)
        out[sync_index].to_numpy()
        result.append((time.monotonic() - t0) * 1e3)
    return result, out


dev = Accelerator()
ws = load_weights([Path(CKPT)])
w, s = ws["blocks.0.mlp.up.weight"].data(), ws["blocks.0.mlp.up.weight_scale"].data()
x = np.random.default_rng(0).standard_normal((M, K), dtype=np.float32)
xt = Tensor.from_dlpack(x).to(dev)

with F.lazy():
    quant = Quantize()
quant_run = quant.compile(TensorType(DType.float32, [M, K], dev))
q_times, (xq, xs) = timings(quant_run, (xt,))
print("quantize ms:", [round(t, 1) for t in q_times])

with F.lazy():
    mma = MMA().to(dev)
t0 = time.monotonic()
mma_run = mma.compile(
    TensorType(DType.float8_e4m3fn, [M, K], dev),
    TensorType(DType.float32, [1, M], dev),
    weights={"weight": w, "weight_scale": s},
    custom_extensions=[krea2.KERNELS],
)
print(f"MMA compile: {time.monotonic() - t0:.1f}s")
mma_times, out = timings(mma_run, (xq, xs), sync_index=1)
print("MMA+scale ms:", [round(t, 1) for t in mma_times])
np.save("/tmp/micro_mma.npy", out[0].to_numpy())
