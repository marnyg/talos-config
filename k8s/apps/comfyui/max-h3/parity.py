#!/usr/bin/env python3
"""Run the MAX H3 Image DiT against Comfy references or synthetic shapes."""
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
ap.add_argument("--weights", required=True, help="export written by ref_dump.py")
ap.add_argument("--ref", help="optional reference .npz written by ref_dump.py")
ap.add_argument("--layers", type=int, default=2)
ap.add_argument("--quant", choices=("bf16", "w4a8"), default="bf16")
ap.add_argument("--ckpt", help="original packed W4A8 checkpoint (required for --quant w4a8)")
ap.add_argument("--latent", type=int, help="synthetic latent H=W when --ref is omitted")
ap.add_argument("--txt", type=int, default=256, help="synthetic text rows when --ref is omitted")
ap.add_argument("--sigma", type=float, default=0.75)
ap.add_argument("--schedule", action="store_true", help="run every PDD sigma after the single forward")
ap.add_argument("--runs", type=int, default=3)
ap.add_argument("--save", help="optionally save MAX output as .npy")
a = ap.parse_args()

ref = np.load(a.ref) if a.ref else None
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

if ref is not None:
    x, context = ref["x"], ref["context"]
    sigma, sample_sigmas = float(ref["sigma"]), ref["sample_sigmas"]
else:
    latent = a.latent or 32
    rng = np.random.default_rng(0)
    x = rng.standard_normal((1, 24, 1, latent, latent), dtype=np.float32)
    context = rng.standard_normal((1, a.txt, 5120), dtype=np.float32) * 3.0
    sigma, sample_sigmas = a.sigma, h3.PDD_SIGMAS


reference_latents = ([ref["reference_latent"]]
                     if ref is not None and "reference_latent" in ref else [])


def prepare(step_sigma):
    return h3.prepare_inputs(
        x, context, step_sigma, sample_sigmas,
        host["adaln_t_table"], host["rope.inv_freq"],
        host["final_layer.video_out.weight"], host["final_layer.video_out.bias"],
        refs=reference_latents,
    )


prepared = prepare(sigma)
if ref is not None and not np.array_equal(prepared["position_ids"], ref["position_ids"]):
    raise AssertionError("MAX host packed layout differs from Comfy's PackedLayout")
print("packed layout prepared; time levels", prepared["unique_t"],
      "sequence", prepared["mod_indices"].shape[0])

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
inputs = [gpu(prepared[name]) for name in input_names]


def timed_run(step_inputs, label):
    t0 = time.monotonic()
    result = run(*step_inputs)
    dev.synchronize()
    elapsed = time.monotonic() - t0
    print(f"{label}: {elapsed:.3f}s")
    return result, elapsed


result = None
for i in range(a.runs):
    result = None  # release the previous execution before allocating the next
    result, _ = timed_run(inputs, f"run {i}")
rows = result.to_numpy()
out = h3.unpatchify(rows, x.shape[-2], x.shape[-1])
if a.save:
    np.save(a.save, out)


def metrics(name, candidate, expected):
    cosine = float((candidate * expected).sum() /
                   (np.linalg.norm(candidate) * np.linalg.norm(expected)))
    rel_l2 = np.linalg.norm(candidate - expected) / np.linalg.norm(expected)
    print(f"{name}: cosine {cosine:.8f}  rel-L2 {rel_l2:.6g}  "
          f"max|d| {np.abs(candidate - expected).max():.6g}")


print("max ", out.shape, "mean", out.mean(), "std", out.std(), dev.stats)
if ref is not None:
    for name in ("out_bf16", "out_w4a8"):
        if name in ref:
            metrics(name, out, ref[name])

if a.schedule:
    if ref is not None and "schedule_sigmas" in ref:
        schedule_sigmas = ref["schedule_sigmas"]
    else:
        schedule_sigmas = sample_sigmas[:-1]
    for i, step_sigma in enumerate(schedule_sigmas):
        step = prepare(float(step_sigma))
        # Layout/RoPE/modality indices are fixed for this packed signature;
        # only the curve coordinate and selected PDD head change per step.
        inputs[2] = gpu(step["t_emb"])
        inputs[7] = gpu(step["pdd_weight"])
        inputs[8] = gpu(step["pdd_bias"])
        result = None
        result, _ = timed_run(inputs, f"schedule {i} sigma {float(step_sigma):.6f}")
        if ref is not None and "schedule_out_w4a8" in ref:
            step_rows = result.to_numpy()
            step_out = h3.unpatchify(step_rows, x.shape[-2], x.shape[-1])
            metrics(f"schedule {i}", step_out, ref["schedule_out_w4a8"][i])
