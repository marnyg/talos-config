#!/usr/bin/env python3
"""Reference for the MAX port: ComfyUI's own Krea 2 DiT, truncated to the
first N blocks, float32 on the CPU, fixed random inputs -> .npz.

Run with the ComfyUI venv:
    ~/disks/1TB-2024/ai/venv/bin/python ref_dump.py --layers 2 --out /tmp/krea2-ref-2.npz
"""
import argparse
import os
import sys

os.environ.setdefault("CUDA_VISIBLE_DEVICES", "")
ap = argparse.ArgumentParser()
ap.add_argument("--comfy", default=os.path.expanduser("~/disks/1TB-2024/ai/ComfyUI"))
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors"))
ap.add_argument("--layers", type=int, default=2)
ap.add_argument("--latent", type=int, default=32, help="latent H=W (image = 8x)")
ap.add_argument("--txt", type=int, default=16)
ap.add_argument("--out", required=True)
a = ap.parse_args()
sys.argv = sys.argv[:1] + ["--cpu"]  # comfy.cli_args parses argv on import
sys.path.insert(0, a.comfy)

import numpy as np  # noqa: E402
import torch  # noqa: E402
from safetensors import safe_open  # noqa: E402

import comfy.options  # noqa: E402

comfy.options.enable_args_parsing()  # or --cpu is ignored and comfy initialises CUDA
import comfy.ldm.krea2.model as km  # noqa: E402
import comfy.ops  # noqa: E402

sd = {}
with safe_open(a.ckpt, "pt") as f:
    for k in f.keys():
        if k.startswith("blocks.") and int(k.split(".")[1]) >= a.layers:
            continue
        sd[k] = f.get_tensor(k)
for k in [k for k in sd if k.endswith(".weight_scale")]:  # fp8_scaled -> float32
    w = k[: -len("_scale")]
    sd[w] = sd[w].float() * sd.pop(k).float()
sd = {k: v.float() for k, v in sd.items()}

m = km.SingleStreamDiT(layers=a.layers, dtype=torch.float32, device="cpu", operations=comfy.ops.disable_weight_init)
print(m.load_state_dict(sd, strict=True))
m.eval()

g = torch.Generator().manual_seed(0)
x = torch.randn(1, 16, a.latent, a.latent, generator=g)
ctx = torch.randn(1, a.txt, 12 * 2560, generator=g) * 3.0
t = torch.tensor([0.7])
with torch.no_grad():
    out = m(x, t, ctx)
print("out", tuple(out.shape), "mean", out.mean().item(), "std", out.std().item())
np.savez(a.out, x=x.numpy(), context=ctx.numpy(), t=t.numpy(), out=out.numpy())
