#!/usr/bin/env python3
"""Run the MAX port on the inputs ref_dump.py saved and compare.

Run with the MAX venv:
    ~/disks/1TB-2024/ai/max-venv/bin/python parity.py --layers 2 --ref /tmp/krea2-ref-2.npz
"""
import argparse
import os
import time
from pathlib import Path

import numpy as np
from max.driver import Accelerator
from max.experimental import functional as F
from max.experimental.tensor import Tensor
from max.graph.weights import load_weights

import krea2

ap = argparse.ArgumentParser()
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors"))
ap.add_argument("--layers", type=int, default=2)
ap.add_argument(
    "--quant",
    choices=("fp8_scaled", "fp8_mojo", "fp8_mojo_fused", "fp8_mojo_tc"),
    default="fp8_scaled",
)
ap.add_argument("--ref", required=True)
ap.add_argument("--runs", type=int, default=3)
a = ap.parse_args()

dev = Accelerator()
print("device", dev, dev.stats)

t0 = time.monotonic()
sd = {}
for k, w in load_weights([Path(a.ckpt)]).items():
    if k.startswith("blocks.") and int(k.split(".")[1]) >= a.layers:
        continue
    sd[k] = w.data()
sd = krea2.remap_keys(sd)
print(f"weights mapped: {len(sd)} tensors, {time.monotonic() - t0:.1f}s")

t0 = time.monotonic()
with F.lazy():
    model = krea2.Krea2DiT(layers=a.layers, quant=a.quant)
    model.to(dev)
missing = set(dict(model.parameters)) - set(sd)
extra = set(sd) - set(dict(model.parameters))
print("missing", sorted(missing)[:8], "extra", sorted(extra)[:8])
run = model.compile(
    *model.input_types(dev),
    weights=sd,
    custom_extensions=[krea2.KERNELS] if a.quant.startswith("fp8_mojo") else (),
)
print(f"compile: {time.monotonic() - t0:.1f}s", dev.stats)

ref = np.load(a.ref)
x, ctx, t = ref["x"], ref["context"], ref["t"]
b, _, h, w = x.shape
txt = ctx.shape[1]
cos, sin = krea2.rope_tables(txt, h // 2, w // 2)


def gpu(arr):
    return Tensor.from_dlpack(np.ascontiguousarray(arr, dtype=np.float32)).to(dev)


inputs = (gpu(krea2.patchify(x)), gpu(ctx.reshape(b, txt, 12, 2560)), gpu(krea2.timestep_embedding(t)),
          gpu(cos), gpu(sin))
for i in range(a.runs):
    t0 = time.monotonic()
    out = run(*inputs).to_numpy()
    print(f"run {i}: {time.monotonic() - t0:.3f}s")
out = krea2.unpatchify(out, h, w)

r = ref["out"]
cosim = float((out * r).sum() / (np.linalg.norm(out) * np.linalg.norm(r)))
print("max ", out.shape, "mean", out.mean(), "std", out.std())
print("ref ", r.shape, "mean", r.mean(), "std", r.std())
print(f"cosine {cosim:.6f}  rel-L2 {np.linalg.norm(out - r) / np.linalg.norm(r):.4f}  max|d| {np.abs(out - r).max():.3f}")
