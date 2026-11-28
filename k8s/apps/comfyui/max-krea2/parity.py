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
ap.add_argument("--ckpt", help="defaults to the checkpoint matching --quant")
ap.add_argument("--layers", type=int, default=2)
ap.add_argument(
    "--quant",
    choices=(
        "fp8_scaled", "fp8_mojo", "fp8_mojo_fused", "fp8_mojo_tc",
        "fp8_mojo_direct", "nvfp4", "nvfp4_mojo_tc",
    ),
    default="fp8_scaled",
)
ap.add_argument("--ref", help="reference .npz; omit for a synthetic-shape benchmark")
ap.add_argument("--latent", type=int, help="latent H=W without --ref (default: 128 = 1024px)")
ap.add_argument("--txt", type=int, help="text tokens without --ref (default: 256)")
ap.add_argument("--save", help="optionally save the unpatchified output as .npy")
ap.add_argument("--runs", type=int, default=3)
a = ap.parse_args()
if not a.ckpt:
    name = "krea2_turbo_nvfp4.safetensors" if a.quant.startswith("nvfp4") else "krea2_turbo_fp8_scaled.safetensors"
    a.ckpt = os.path.expanduser(f"~/disks/1TB-2024/ai/models/diffusion_models/{name}")

dev = Accelerator()
print("device", dev, dev.stats)

t0 = time.monotonic()
sd = {}
for k, w in load_weights([Path(a.ckpt)]).items():
    if k.startswith("blocks.") and int(k.split(".")[1]) >= a.layers:
        continue
    w = w.data()
    if a.quant.startswith("nvfp4"):
        w = krea2.prepare_nvfp4_weight(k, w)
    sd[k] = w
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
    custom_extensions=[krea2.KERNELS] if a.quant.startswith(("fp8_mojo", "nvfp4")) else (),
)
print(f"compile: {time.monotonic() - t0:.1f}s", dev.stats)

if a.ref:
    ref = np.load(a.ref)
    x, ctx, t = ref["x"], ref["context"], ref["t"]
else:
    latent, txt_len = a.latent or 128, a.txt or 256
    rng = np.random.default_rng(0)
    x = rng.standard_normal((1, 16, latent, latent), dtype=np.float32)
    ctx = rng.standard_normal((1, txt_len, 12 * 2560), dtype=np.float32) * 3.0
    t = np.array([0.7], dtype=np.float32)
b, _, h, w = x.shape
txt = ctx.shape[1]
print(f"input: latent={h}x{w}, text={txt}, image_tokens={(h // 2) * (w // 2)}")
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
if a.save:
    np.save(a.save, out)

print("max ", out.shape, "mean", out.mean(), "std", out.std())
if a.ref:
    r = ref["out"]
    cosim = float((out * r).sum() / (np.linalg.norm(out) * np.linalg.norm(r)))
    print("ref ", r.shape, "mean", r.mean(), "std", r.std())
    print(f"cosine {cosim:.6f}  rel-L2 {np.linalg.norm(out - r) / np.linalg.norm(r):.4f}  max|d| {np.abs(out - r).max():.3f}")
