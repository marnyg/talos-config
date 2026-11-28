#!/usr/bin/env python3
"""ComfyUI-comfy-kitchen references for micro_nvfp4.py (ComfyUI venv)."""
import os
import time

import numpy as np
import torch
from safetensors import safe_open

import comfy_kitchen as ck

CKPT = os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_nvfp4.safetensors"
)
KEY = "blocks.0.mlp.up"
M, K, N = 4352, 6144, 16384

with safe_open(CKPT, framework="pt", device="cuda") as f:
    weight = f.get_tensor(KEY + ".weight")
    block_scale = f.get_tensor(KEY + ".weight_scale")
    weight_scale = f.get_tensor(KEY + ".weight_scale_2")

x_np = np.random.default_rng(0).standard_normal((M, K), dtype=np.float32)
x = torch.from_numpy(x_np).cuda().bfloat16()

# full_precision_matrix_mult=True: BF16 activation, dequantized BF16 weight.
dequant = ck.dequantize_nvfp4(weight, weight_scale, block_scale, torch.bfloat16)
for _ in range(2):
    expected_w4a16 = x @ dequant.T
torch.cuda.synchronize()
ts = []
for _ in range(8):
    t0 = time.monotonic()
    expected_w4a16 = x @ dequant.T
    torch.cuda.synchronize()
    ts.append(time.monotonic() - t0)
print(f"comfy w4a16 GEMM ms {[round(t * 1e3, 1) for t in ts]}")
np.save("/tmp/micro_comfy_nvfp4_w4a16.npy", expected_w4a16[:64, :64].float().cpu().numpy())
del dequant, expected_w4a16

# full_precision_matrix_mult=False: dynamically quantized NVFP4 activation.
input_scale = x.abs().amax().float() / (448 * 6)
for _ in range(2):
    qx, x_block_scale = ck.quantize_nvfp4(x, input_scale)
    expected_w4a4 = ck.scaled_mm_nvfp4(
        qx, weight, input_scale, weight_scale, x_block_scale, block_scale
    )
torch.cuda.synchronize()
ts = []
for _ in range(8):
    t0 = time.monotonic()
    qx, x_block_scale = ck.quantize_nvfp4(x, input_scale)
    expected_w4a4 = ck.scaled_mm_nvfp4(
        qx, weight, input_scale, weight_scale, x_block_scale, block_scale
    )
    torch.cuda.synchronize()
    ts.append(time.monotonic() - t0)
print(f"comfy w4a4 quant+GEMM ms {[round(t * 1e3, 1) for t in ts]}")
np.save("/tmp/micro_comfy_nvfp4_w4a4.npy", expected_w4a4[:64, :64].float().cpu().numpy())

# Isolate MAX's host-scalar limitation: its device graph must use unit global scale.
unit_scale = torch.ones((), device=x.device, dtype=torch.float32)
qx, x_block_scale = ck.quantize_nvfp4(x, unit_scale)
expected_unit = ck.scaled_mm_nvfp4(
    qx, weight, unit_scale, weight_scale, x_block_scale, block_scale
)
np.save("/tmp/micro_comfy_nvfp4_unit.npy", expected_unit[:64, :64].float().cpu().numpy())
