#!/usr/bin/env python3
"""One real H3 W4A8 projection with every intermediate needed by the Mojo tests."""
import argparse
import os
import statistics

import numpy as np
import torch
from safetensors import safe_open

import comfy_kitchen as ck
from comfy_kitchen.backends.cuda import (
    _dequant_int4_grouped_to_int8,
    quantize_int8_rowwise_convrot,
)
from comfy_kitchen.tensor.int8_utils import _build_hadamard, _rotate_activation
from comfy_kitchen.tensor.w4a8_int8 import (
    dequantize_w4a8_int8_weight,
    w4a8_int8_linear,
)

ap = argparse.ArgumentParser()
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/minimax_h3_image_pruned_w4a8.safetensors"))
ap.add_argument("--layer", default="blocks.0.attn.out_proj")
ap.add_argument("--rows", type=int, default=64)
ap.add_argument("--out", default="/tmp/h3-w4a8-ref.npz")
ap.add_argument("--runs", type=int, default=16)
a = ap.parse_args()

with safe_open(a.ckpt, "pt", device="cuda") as f:
    qdata = f.get_tensor(a.layer + ".weight")
    s_rel = f.get_tensor(a.layer + ".weight_s_rel").view(torch.float8_e4m3fn)
    s_channel = f.get_tensor(a.layer + ".weight_s_channel")
    codebook = f.get_tensor(a.layer + ".weight_codebook")
n, packed_k = qdata.shape
k = packed_k * 2
g = torch.Generator().manual_seed(7)
x = (torch.randn(a.rows, k, generator=g) * 2.0).cuda().bfloat16()
with torch.inference_mode():
    xq, xs = quantize_int8_rowwise_convrot(x, 256)
    wq = _dequant_int4_grouped_to_int8(qdata, s_rel, codebook, 16)
    acc = torch._int_mm(xq, wq.t())
    out = w4a8_int8_linear(
        x, qdata, s_rel, s_channel, codebook=codebook,
        group_size=16, convrot_groupsize=256, out_dtype=torch.bfloat16,
    )
    recomposed = (acc.float() * xs * s_channel).bfloat16()
    h = _build_hadamard(256, device=x.device, dtype=x.dtype)
    rotated = _rotate_activation(x, h, 256)
    xq_unfused, xs_unfused = ck.quantize_int8_rowwise(rotated)

print("layer", a.layer, "M/N/K", a.rows, n, k)
print("xq range", xq.min().item(), xq.max().item(), "xs", xs.min().item(), xs.max().item())
print("wq range", wq.min().item(), wq.max().item())
print("recomposed max|d|", (recomposed - out).abs().max().item())
np.savez(
    a.out,
    x=x.float().cpu().numpy(),
    rotated=rotated.float().cpu().numpy(),
    xq=xq.cpu().numpy(),
    xs=xs.cpu().numpy(),
    wq=wq.cpu().numpy(),
    acc=acc.cpu().numpy(),
    s_channel=s_channel.cpu().numpy(),
    out=out.float().cpu().numpy(),
    recomposed=recomposed.float().cpu().numpy(),
    xq_unfused=xq_unfused.cpu().numpy(),
    xs_unfused=xs_unfused.cpu().numpy(),
    layer=np.asarray(a.layer),
)
print("saved", a.out)


def bench(name, fn):
    for _ in range(3):
        fn()
    times = []
    for _ in range(a.runs):
        start, end = torch.cuda.Event(True), torch.cuda.Event(True)
        start.record()
        fn()
        end.record()
        end.synchronize()
        times.append(start.elapsed_time(end))
    print(f"{name}: median {statistics.median(times):.3f} ms; "
          f"min {min(times):.3f}; max {max(times):.3f}")


with torch.inference_mode():
    bench("Comfy W4A8", lambda: w4a8_int8_linear(
        x, qdata, s_rel, s_channel, codebook=codebook,
        group_size=16, convrot_groupsize=256, out_dtype=torch.bfloat16,
    ))
    weight_bf16 = dequantize_w4a8_int8_weight(
        qdata, s_rel, s_channel, codebook=codebook,
        group_size=16, convrot_groupsize=256, output_dtype=torch.bfloat16,
    )
    bench("Comfy dequant+BF16 GEMM", lambda: torch.nn.functional.linear(x, weight_bf16))
