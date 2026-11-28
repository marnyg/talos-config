#!/usr/bin/env python3
"""ComfyUI/PyTorch side of micro_fp8.py; run with the ComfyUI venv.

Shares /tmp/krea-micro-x.npy with the MAX benchmark and writes the same
64x64 output slice to /tmp/micro_comfy.npy for numerical comparison.
"""
import os
import time
from pathlib import Path

import numpy as np
import torch
from safetensors import safe_open

CKPT = os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_fp8_scaled.safetensors"
)
M, K, N = 4352, 6144, 16384
X_PATH = Path("/tmp/krea-micro-x.npy")
if X_PATH.exists():
    x_np = np.load(X_PATH)
else:
    x_np = np.random.default_rng(0).standard_normal((M, K), dtype=np.float32)
    np.save(X_PATH, x_np)

with safe_open(CKPT, "pt") as f:
    weight = f.get_tensor("blocks.0.mlp.up.weight").cuda()
    scale = f.get_tensor("blocks.0.mlp.up.weight_scale").cuda()
weight_bf16 = (weight.to(torch.bfloat16) * scale.to(torch.bfloat16)).contiguous()
del weight, scale
x = torch.from_numpy(x_np).cuda().to(torch.bfloat16)

times = []
for _ in range(9):
    torch.cuda.synchronize()
    t0 = time.monotonic()
    y = x @ weight_bf16.T
    mean = y.mean().item()
    torch.cuda.synchronize()
    times.append((time.monotonic() - t0) * 1e3)

warm = times[1:]
print("ComfyUI/PyTorch ms (first is warmup):", [round(t, 1) for t in times])
print(f"warm median: {np.median(warm):.2f} ms; mean(y)={mean:.8g}")
np.save("/tmp/micro_comfy.npy", y[:64, :64].float().cpu().numpy())
