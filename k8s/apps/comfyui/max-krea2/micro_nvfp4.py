#!/usr/bin/env python3
"""Benchmark one real Krea NVFP4 projection at the 1024^2 sequence length."""
import os
import sys
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

CKPT = os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_nvfp4.safetensors"
)
KEY = "blocks.0.mlp.up"
M, K, N = 4352, 6144, 16384
which = sys.argv[1] if len(sys.argv) == 2 else ""
if which not in ("mojo", "dequant", "native"):
    raise SystemExit(f"usage: {sys.argv[0]} mojo|dequant|native")


class Wrap(Module[[Tensor], tuple[Tensor, Tensor]]):
    def __init__(self) -> None:
        if which == "mojo":
            self.lin = krea2.MojoTensorCoreNVFP4Linear(K, N)
        elif which == "dequant":
            self.lin = krea2.MojoDequantNVFP4Linear(K, N)
        else:
            self.lin = krea2.NativeNVFP4Linear(K, N)

    def forward(self, x: Tensor) -> tuple[Tensor, Tensor]:
        y = self.lin(x).cast(DType.float32)
        return y[:64, :64], F.mean(y)


dev = Accelerator()
weights = load_weights([Path(CKPT)])
sd = {}
for suffix in ("weight", "weight_scale", "weight_scale_2"):
    key = f"{KEY}.{suffix}"
    value = weights[key].data()
    sd[f"lin.{suffix}"] = krea2.prepare_nvfp4_weight(key, value)

with F.lazy():
    model = Wrap()
    model.to(dev)
free0 = dev.stats["free_memory"]
t0 = time.monotonic()
run = model.compile(
    TensorType(DType.float32, [M, K], dev),
    weights=sd,
    custom_extensions=[krea2.KERNELS] if which in ("mojo", "dequant") else (),
)
print(
    f"{which}: compile {time.monotonic() - t0:.1f}s, "
    f"allocation delta {(free0 - dev.stats['free_memory']) / 2**20:.0f} MiB",
    flush=True,
)
x = np.random.default_rng(0).standard_normal((M, K), dtype=np.float32)
xt = Tensor.from_dlpack(x).to(dev)
ts = []
for i in range(8):
    t0 = time.monotonic()
    ys, mean = run(xt)
    mean = float(mean.to_numpy().mean())
    ts.append(time.monotonic() - t0)
print(f"{which}: runs ms {[round(t * 1e3, 1) for t in ts]}  mean(y)={mean:.5f}")
np.save(f"/tmp/micro_nvfp4_{which}.npy", ys.to_numpy())
