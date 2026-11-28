#!/usr/bin/env python3
"""One Krea 2 MLP projection (16384x6144, FP8) at 1024^2 sequence length,
three ways: bf16 GEMM on the widened weight, MAX's native FP8 GEMM with
dynamically quantized activations (what its Ideogram 4 port uses), and
the memory each leaves on the card.  Run with the MAX venv."""
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

CKPT = os.path.expanduser("~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors")
KEY = "blocks.0.mlp.up"
M, K, N = 4352, 6144, 16384  # 4096 image tokens + 256 text at 1024^2
which = sys.argv[1] if len(sys.argv) == 2 else ""
if which not in ("input", "bf16", "fp8", "mojo", "fused", "tc"):
    raise SystemExit(f"usage: {sys.argv[0]} input|bf16|fp8|mojo|fused|tc")


class Wrap(Module[[Tensor], tuple[Tensor, Tensor]]):
    def __init__(self, lin: Module) -> None:
        self.lin = lin

    def forward(self, x: Tensor) -> tuple[Tensor, Tensor]:
        y = self.lin(x.cast(DType.bfloat16)).cast(DType.float32)
        return y[:64, :64], F.mean(y)


class WrapIn(Module[..., tuple[Tensor, Tensor]]):
    """The FP8 weight as a graph INPUT, so nothing can be precomputed at load."""

    def forward(self, x: Tensor, w: Tensor, scale: Tensor) -> tuple[Tensor, Tensor]:
        wb = w.cast(DType.bfloat16) * scale.cast(DType.bfloat16)
        y = (x.cast(DType.bfloat16) @ wb.T).cast(DType.float32)
        return y[:64, :64], F.mean(y)


dev = Accelerator()
ws = load_weights([Path(CKPT)])
w, s = ws[KEY + ".weight"].data(), ws[KEY + ".weight_scale"].data()
scale = float(np.from_dlpack(s))
free0 = dev.stats["free_memory"]
with F.lazy():
    extra = ()
    if which == "input":
        m, sd = WrapIn(), {}
        extra = (Tensor.from_dlpack(w).to(dev), Tensor.from_dlpack(s).to(dev))
        types = (TensorType(DType.float8_e4m3fn, [N, K], dev), TensorType(DType.float32, [], dev))
    elif which == "bf16":
        m = Wrap(krea2.ScaledFP8Linear(K, N))
        sd = {"lin.weight": w, "lin.weight_scale": s}
    elif which == "mojo":
        m = Wrap(krea2.MojoScaledFP8Linear(K, N))
        sd = {"lin.weight": w, "lin.weight_scale": s}
    elif which == "fused":
        m = Wrap(krea2.MojoFusedFP8Linear(K, N))
        sd = {"lin.weight": w, "lin.weight_scale": s}
    elif which == "tc":
        m = Wrap(krea2.MojoTensorCoreFP8Linear(K, N))
        sd = {"lin.weight": w, "lin.weight_scale": s}
    else:
        # Importing max.pipelines pulls in its model registry and optional
        # dependencies; only pay that cost for the native-FP8 comparison.
        from max.pipelines.architectures.ideogram4.layers.fp8_linear import Ideogram4FP8Linear
        m = Wrap(Ideogram4FP8Linear(K, N))
        sd = {"lin.weight": w, "lin.weight_scale": np.full((N, 1), scale, dtype=np.float32)}
    m.to(dev)
t0 = time.monotonic()
run = m.compile(
    TensorType(DType.float32, [M, K], dev),
    *(types if which == "input" else ()),
    weights=sd,
    custom_extensions=[krea2.KERNELS] if which in ("mojo", "fused", "tc") else (),
)
print(f"{which}: compile {time.monotonic() - t0:.1f}s, weight on card {(free0 - dev.stats['free_memory']) / 2**20:.0f} MiB"
      f" (FP8 file size {N * K / 2**20:.0f} MiB)")
x = np.random.default_rng(0).standard_normal((M, K), dtype=np.float32)
xt = Tensor.from_dlpack(x).to(dev)
ts = []
for i in range(8):
    t0 = time.monotonic()
    ys, mean = run(xt, *extra)
    mean = float(mean.to_numpy().mean())
    ts.append(time.monotonic() - t0)
print(f"{which}: runs ms {[round(t * 1e3, 1) for t in ts]}  mean(y)={mean:.5f}")
np.save(f"/tmp/micro_{which}.npy", ys.to_numpy())
