#!/usr/bin/env python3
"""ComfyUI NVFP4 GPU reference for the MAX port (ComfyUI venv)."""
import argparse
import json
import os
import sys

ap = argparse.ArgumentParser()
ap.add_argument("--comfy", default=os.path.expanduser("~/disks/1TB-2024/ai/ComfyUI"))
ap.add_argument("--ckpt", default=os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_nvfp4.safetensors"))
ap.add_argument("--layers", type=int, default=2)
ap.add_argument("--latent", type=int, default=32)
ap.add_argument("--txt", type=int, default=64)
ap.add_argument("--unit-scale", action="store_true", help="match MAX's required unit global activation scale")
ap.add_argument("--w4a16", action="store_true", help="dequantize every layer and keep BF16 activations")
ap.add_argument("--debug-block", action="store_true", help="save block 0 input, modulation and output")
ap.add_argument("--out", required=True)
a = ap.parse_args()
sys.argv = sys.argv[:1]
sys.path.insert(0, a.comfy)

import numpy as np  # noqa: E402
import torch  # noqa: E402
from safetensors import safe_open  # noqa: E402

import comfy.ldm.krea2.model as km  # noqa: E402
import comfy.ops  # noqa: E402
import comfy.quant_ops  # noqa: E402  # registers the NVFP4 layout

sd = {}
with safe_open(a.ckpt, "pt", device="cuda") as f:
    metadata = json.loads(f.metadata()["_quantization_metadata"])["layers"]
    for k in f.keys():
        if k.startswith("blocks.") and int(k.split(".")[1]) >= a.layers:
            continue
        sd[k] = f.get_tensor(k)
for layer, config in metadata.items():
    if layer.startswith("blocks.") and int(layer.split(".")[1]) >= a.layers:
        continue
    # _load_quantized_module decodes this JSON with numpy(), so keep it on CPU.
    sd[layer + ".comfy_quant"] = torch.tensor(
        list(json.dumps(config).encode("utf-8")), dtype=torch.uint8
    )

operations = comfy.ops.mixed_precision_ops(
    compute_dtype=torch.bfloat16, full_precision_mm=a.w4a16
)
m = km.SingleStreamDiT(
    layers=a.layers, dtype=torch.bfloat16, device="cuda", operations=operations
)
print(m.load_state_dict(sd, strict=True))
m.eval()
if a.unit_scale:
    for module in m.modules():
        if (getattr(module, "quant_format", None) == "nvfp4"
                and not module._full_precision_mm):
            module.input_scale = torch.nn.Parameter(
                torch.ones((), device="cuda", dtype=torch.float32), requires_grad=False
            )

debug = {}
if a.debug_block:
    def save_block_input(module, args):
        debug["block_in"] = args[0].float().cpu().numpy()
        debug["vec"] = args[1].float().cpu().numpy()

    def save_block_output(module, args, output):
        debug["block_out"] = output.float().cpu().numpy()

    def save_component(name):
        def save_input(module, args):
            debug[name + "_in"] = args[0].float().cpu().numpy()

        def save_output(module, args, output):
            debug[name + "_out"] = output.float().cpu().numpy()

        return save_input, save_output

    m.blocks[0].register_forward_pre_hook(save_block_input)
    m.blocks[0].register_forward_hook(save_block_output)
    for name in ("attn", "mlp"):
        save_input, save_output = save_component(name)
        component = getattr(m.blocks[0], name)
        component.register_forward_pre_hook(save_input)
        component.register_forward_hook(save_output)

g = torch.Generator().manual_seed(0)
x = torch.randn(1, 16, a.latent, a.latent, generator=g)
ctx = torch.randn(1, a.txt, 12 * 2560, generator=g) * 3.0
t = torch.tensor([0.7])
with torch.inference_mode():
    out = m(x.cuda().bfloat16(), t.cuda(), ctx.cuda().bfloat16())
print("out", tuple(out.shape), "mean", out.float().mean().item(), "std", out.float().std().item())
np.savez(a.out, x=x.numpy(), context=ctx.numpy(), t=t.numpy(), out=out.float().cpu().numpy(), **debug)
