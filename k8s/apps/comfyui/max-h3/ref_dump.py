#!/usr/bin/env python3
"""Real H3 ComfyUI references plus BF16-dequantized weights for the MAX port.

Run with ComfyUI's venv. Depending on --reference-mode, the .npz contains the
BF16-dequantized and/or actual W4A8 result plus an optional PDD schedule.
--export-mode controls whether quantized projections are also dequantized into
the .safetensors export consumed by parity.py.
"""
import argparse
import json
import os
import statistics
import sys
from pathlib import Path

ap = argparse.ArgumentParser()
ap.add_argument("--comfy", default=os.path.expanduser("~/disks/1TB-2024/ai/ComfyUI"))
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/minimax_h3_image_pruned_w4a8.safetensors"))
ap.add_argument("--layers", type=int, default=2)
ap.add_argument("--latent", type=int, default=32, help="latent H=W; 32 gives 256 image tokens")
ap.add_argument("--txt", type=int, default=64)
ap.add_argument("--ref-latent", type=int, help="add one synthetic reference image latent of this H=W")
ap.add_argument("--sigma", type=float, default=0.75)
ap.add_argument("--schedule", action="store_true", help="also run every PDD grid step")
ap.add_argument("--benchmark", type=int, default=0, metavar="N", help="time N actual-W4A8 forwards")
ap.add_argument("--reference-mode", choices=("both", "bf16", "w4a8"), default="both")
ap.add_argument("--export-mode", choices=("all", "unquantized"), default="all")
ap.add_argument("--debug-blocks", type=int, default=2)
ap.add_argument("--out", required=True, help="reference .npz")
ap.add_argument("--weights", required=True, help="non-quantized/BF16 .safetensors export")
a = ap.parse_args()
sys.argv = sys.argv[:1]
sys.path.insert(0, a.comfy)

import numpy as np  # noqa: E402
import torch  # noqa: E402
from safetensors import safe_open  # noqa: E402
from safetensors.torch import save_file  # noqa: E402

import comfy.ldm.minimax.model as hm  # noqa: E402
import comfy.ops  # noqa: E402
import comfy.quant_ops  # noqa: E402
from comfy.quant_ops import QuantizedTensor  # noqa: E402

with safe_open(a.ckpt, "pt", device="cpu") as f:
    config = json.loads(f.metadata()["config"])["transformer"]
    pdd_sigmas = np.asarray(config["pdd_sigmas"], dtype=np.float32)
    pdd_steps = int(config["pdd_steps"])

sd = {}
with safe_open(a.ckpt, "pt", device="cuda") as f:
    for key in f.keys():
        if key.startswith("blocks.") and int(key.split(".")[1]) >= a.layers:
            continue
        value = f.get_tensor(key)
        # Both JSON metadata decoders call numpy() directly.
        if key.endswith((".comfy_quant", ".attn.comfy_attention.config")):
            value = value.cpu()
        sd[key] = value

operations = comfy.ops.mixed_precision_ops(
    compute_dtype=torch.bfloat16, full_precision_mm=True
)
model = hm.MiniMaxH3ImageModel(
    num_layers=a.layers,
    adaln_curve_grid=1025,
    time_embed_dim=8,
    pdd_sigmas=pdd_sigmas.tolist(),
    pdd_steps=pdd_steps,
    dtype=torch.bfloat16,
    device="cuda",
    operations=operations,
)
print(model.load_state_dict(sd, strict=True))
model.requires_grad_(False)  # fused in-place RoPE rejects grad-enabled q/k/norm tensors
model.eval()

# Export the exact weights used by the full_precision_mm=True path. Keep the
# FP32 islands and all non-quantized tensors at their checkpoint dtypes.
quant_modules = {
    name: module for name, module in model.named_modules()
    if isinstance(getattr(module, "weight", None), QuantizedTensor)
}
print(f"found {len(quant_modules)} W4A8 projections; export mode: {a.export_mode}")
quant_aux = (".weight_s_rel", ".weight_s_channel", ".weight_codebook",
             ".weight_correction", ".comfy_quant")
export = {}
for key, value in sd.items():
    if key.endswith(quant_aux) or key.endswith(".attn.comfy_attention.config"):
        continue
    if any(key == name + ".weight" for name in quant_modules):
        continue
    export[key] = value.cpu()
if a.export_mode == "all":
    for i, (name, module) in enumerate(quant_modules.items()):
        export[name + ".weight"] = module.weight.dequantize().cpu()
        if (i + 1) % 4 == 0:
            print(f"  dequantized {i + 1}/{len(quant_modules)}")
Path(a.weights).parent.mkdir(parents=True, exist_ok=True)
save_file(export, a.weights, metadata={"format": f"h3-{a.export_mode}-v1"})
print(f"saved {len(export)} tensors to {a.weights}")
del export

# Fixed Qwen-like context and latent. No text encoder or VAE is involved.
g = torch.Generator().manual_seed(0)
x = torch.randn(1, 24, 1, a.latent, a.latent, generator=g)
context = torch.randn(1, a.txt, 5120, generator=g) * 3.0
ref_latent = None
ref_blocks = []
if a.ref_latent:
    ref_latent = torch.randn(1, 24, 1, a.ref_latent, a.ref_latent, generator=g)
    ref_blocks.append({
        "kind": "image", "latent_h": a.ref_latent, "latent_w": a.ref_latent,
        "latent": ref_latent,
    })
layout = hm.PackedLayout(a.txt, 1, a.latent, a.latent, 0, refs=ref_blocks)
sample_sigmas = pdd_sigmas.copy()
payload = {
    "layout": layout,
    "refs": ref_blocks,
    "cond_video_latents": [ref_latent] if ref_latent is not None else [],
}
options = {"sample_sigmas": torch.from_numpy(sample_sigmas).cuda()}
timestep = torch.tensor([a.sigma * 1000.0], dtype=torch.float32, device="cuda")
x_gpu, context_gpu = x.cuda().bfloat16(), context.cuda().bfloat16()

mode = "bf16"
debug = {}

def block_pre(index):
    def hook(module, args):
        if index == 0:
            debug[f"{mode}_block0_in"] = args[0].float().cpu().numpy()
            debug[f"{mode}_t_emb"] = args[1].float().cpu().numpy()
    return hook


def block_post(index):
    def hook(module, args, output):
        debug[f"{mode}_block{index}_out"] = output.float().cpu().numpy()
    return hook


for i, block in enumerate(model.blocks[:a.debug_blocks]):
    block.register_forward_pre_hook(block_pre(i))
    block.register_forward_hook(block_post(i))
model.token_refiner.register_forward_hook(
    lambda module, args, output: debug.__setitem__(
        f"{mode}_refiner_out", output.float().cpu().numpy()
    )
)

results = {}
if a.reference_mode in ("both", "bf16"):
    mode = "bf16"
    with torch.inference_mode():
        out_bf16 = model(
            x_gpu, timestep, context_gpu, transformer_options=options,
            minimax_payload=payload,
        )
    results["out_bf16"] = out_bf16.float().cpu().numpy()
    print("bf16", tuple(out_bf16.shape), out_bf16.float().mean().item(),
          out_bf16.float().std().item())

# The same resident QuantizedTensors now execute Comfy's real ConvRot +
# dynamic-INT8 activation + INT8 GEMM path.
for module in model.modules():
    if getattr(module, "quant_format", None) == "asym_w4a8_int8":
        module._full_precision_mm = False
mode = "w4a8"
if a.reference_mode in ("both", "w4a8"):
    with torch.inference_mode():
        out_w4a8 = model(
            x_gpu, timestep, context_gpu, transformer_options=options,
            minimax_payload=payload,
        )
    results["out_w4a8"] = out_w4a8.float().cpu().numpy()
    print("w4a8", tuple(out_w4a8.shape), out_w4a8.float().mean().item(),
          out_w4a8.float().std().item())

if a.schedule:
    schedule = []
    with torch.inference_mode():
        for i, sigma in enumerate(sample_sigmas[:-1]):
            step_t = torch.tensor([float(sigma) * 1000.0], device="cuda")
            out = model(
                x_gpu, step_t, context_gpu, transformer_options=options,
                minimax_payload=payload,
            )
            schedule.append(out.float().cpu().numpy())
            print(f"schedule {i}: sigma {float(sigma):.6f}; "
                  f"mean {out.float().mean().item():.6g}")
    results["schedule_sigmas"] = sample_sigmas[:-1]
    results["schedule_out_w4a8"] = np.stack(schedule)

if a.benchmark:
    with torch.inference_mode():
        for _ in range(2):
            model(x_gpu, timestep, context_gpu, transformer_options=options,
                  minimax_payload=payload)
        torch.cuda.reset_peak_memory_stats()
        times = []
        for _ in range(a.benchmark):
            start, end = torch.cuda.Event(True), torch.cuda.Event(True)
            start.record()
            model(x_gpu, timestep, context_gpu, transformer_options=options,
                  minimax_payload=payload)
            end.record()
            end.synchronize()
            times.append(start.elapsed_time(end))
    print(f"Comfy W4A8 benchmark: median {statistics.median(times):.3f} ms; "
          f"min {min(times):.3f}; max {max(times):.3f}; "
          f"peak allocated {torch.cuda.max_memory_allocated() / 2**30:.3f} GiB")

Path(a.out).parent.mkdir(parents=True, exist_ok=True)
np.savez(
    a.out,
    x=x.numpy(),
    context=context.numpy(),
    sigma=np.asarray(a.sigma, dtype=np.float32),
    sample_sigmas=sample_sigmas,
    pdd_sigmas=pdd_sigmas,
    position_ids=layout.position_ids.numpy(),
    **({"reference_latent": ref_latent.numpy()} if ref_latent is not None else {}),
    **results,
    **debug,
)
print(f"saved reference to {a.out}")
