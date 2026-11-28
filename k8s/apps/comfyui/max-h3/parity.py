#!/usr/bin/env python3
"""Run the MAX H3 Image DiT against ref_dump.py's real ComfyUI references."""
import argparse
import time
from pathlib import Path

import numpy as np
from max.driver import Accelerator
from max.experimental import functional as F
from max.experimental.tensor import Tensor
from max.graph.weights import load_weights

import h3

ap = argparse.ArgumentParser()
ap.add_argument("--weights", required=True, help="BF16 export written by ref_dump.py")
ap.add_argument("--ref", required=True, help="reference .npz written by ref_dump.py")
ap.add_argument("--layers", type=int, default=2)
ap.add_argument("--quant", choices=("bf16", "w4a8"), default="bf16")
ap.add_argument("--ckpt", help="original packed W4A8 checkpoint (required for --quant w4a8)")
ap.add_argument("--runs", type=int, default=3)
ap.add_argument("--save", help="optionally save MAX output as .npy")
a = ap.parse_args()

ref = np.load(a.ref)
dev = Accelerator()
print("device", dev, dev.stats)

t0 = time.monotonic()
weights = load_weights([Path(a.weights)])
host = {}
for key in ("adaln_t_table", "rope.inv_freq", "final_layer.video_out.weight",
            "final_layer.video_out.bias"):
    host[key] = np.array(weights[key].data().to_buffer().to_numpy(), copy=True)
sd = {}
for key, value in weights.items():
    if key.startswith("blocks.") and int(key.split(".")[1]) >= a.layers:
        continue
    if key in host or key.startswith("final_layer.video_out."):
        continue
    if a.quant == "w4a8" and key.startswith("blocks.") and key.endswith(".weight"):
        if any(name in key for name in ("attn.qkv_proj", "attn.out_proj", "mlp.fc1", "mlp.fc2")):
            continue
    sd[key] = value.data()
if a.quant == "w4a8":
    if not a.ckpt:
        ap.error("--quant w4a8 needs --ckpt")
    packed = load_weights([Path(a.ckpt)])
    for key, value in packed.items():
        if not key.startswith("blocks.") or int(key.split(".")[1]) >= a.layers:
            continue
        if not any(name in key for name in ("attn.qkv_proj", "attn.out_proj", "mlp.fc1", "mlp.fc2")):
            continue
        if key.endswith((".weight", ".weight_s_rel", ".weight_s_channel", ".weight_codebook")):
            sd[key] = value.data()
print(f"weights mapped: {len(sd)} tensors, {time.monotonic() - t0:.1f}s")

prepared = h3.prepare_inputs(
    ref["x"], ref["context"], float(ref["sigma"]), ref["sample_sigmas"],
    host["adaln_t_table"], host["rope.inv_freq"],
    host["final_layer.video_out.weight"], host["final_layer.video_out.bias"],
)
if not np.array_equal(prepared["position_ids"], ref["position_ids"]):
    raise AssertionError("MAX host packed layout differs from Comfy's PackedLayout")
print("packed layout: exact match; time levels", prepared["unique_t"])

t0 = time.monotonic()
with F.lazy():
    model = h3.H3ImageDiT(layers=a.layers, quant=a.quant)
    model.to(dev)
missing = set(dict(model.parameters)) - set(sd)
extra = set(sd) - set(dict(model.parameters))
print("missing", sorted(missing)[:8], "extra", sorted(extra)[:8])
if missing or extra:
    raise RuntimeError("checkpoint/module parameter mismatch")
run = model.compile(
    *model.input_types(
        dev,
        visual_rows=prepared["visual"].shape[0],
        text_rows=prepared["context"].shape[0],
        time_levels=prepared["t_emb"].shape[0],
        target_rows=prepared["target_indices"].shape[0],
    ),
    weights=sd,
    custom_extensions=[h3.KERNELS] if a.quant == "w4a8" else (),
)
print(f"compile: {time.monotonic() - t0:.1f}s", dev.stats)


def gpu(array):
    return Tensor.from_dlpack(np.ascontiguousarray(array)).to(dev)


input_names = ("visual", "context", "t_emb", "mod_indices", "target_indices",
               "rope_cos", "rope_sin", "pdd_weight", "pdd_bias")
inputs = tuple(gpu(prepared[name]) for name in input_names)
for i in range(a.runs):
    t0 = time.monotonic()
    rows = run(*inputs).to_numpy()
    print(f"run {i}: {time.monotonic() - t0:.3f}s")
out = h3.unpatchify(rows, ref["x"].shape[-2], ref["x"].shape[-1])
if a.save:
    np.save(a.save, out)

print("max ", out.shape, "mean", out.mean(), "std", out.std())
for name in ("out_bf16", "out_w4a8"):
    expected = ref[name]
    cosine = float((out * expected).sum() /
                   (np.linalg.norm(out) * np.linalg.norm(expected)))
    rel_l2 = np.linalg.norm(out - expected) / np.linalg.norm(expected)
    print(
        f"{name}: cosine {cosine:.8f}  rel-L2 {rel_l2:.6g}  "
        f"max|d| {np.abs(out - expected).max():.6g}; "
        f"ref mean {expected.mean():.6g} std {expected.std():.6g}"
    )
