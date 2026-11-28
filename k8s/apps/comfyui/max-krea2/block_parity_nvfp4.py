#!/usr/bin/env python3
"""Isolate one NVFP4 SingleStreamBlock against ref_dump_nvfp4.py --debug-block."""
import argparse
import os
from pathlib import Path

import numpy as np
from max.driver import Accelerator
from max.dtype import DType
from max.experimental import functional as F
from max.experimental.nn import Module
from max.experimental.tensor import Tensor, TensorType
from max.graph.weights import load_weights

import krea2

CKPT = os.path.expanduser(
    "~/disks/1TB-2024/ai/models/diffusion_models/krea2_turbo_nvfp4.safetensors"
)
REF = "/tmp/krea2-nvfp4-debug-1.npz"
ap = argparse.ArgumentParser()
ap.add_argument("--component", choices=("block", "attn", "mlp"), default="block")
ap.add_argument("--ckpt", default=CKPT)
ap.add_argument("--ref", default=REF)
ap.add_argument("--txt", type=int, default=64)
a = ap.parse_args()


class Wrap(Module[..., Tensor]):
    def __init__(self) -> None:
        if a.component == "block":
            self.block = krea2.SingleStreamBlock(6144, 48, 12, 4, "nvfp4_mojo_tc")
        elif a.component == "attn":
            self.block = krea2.Attention(6144, 48, 12, "nvfp4_mojo_tc")
        else:
            self.block = krea2.SwiGLU(6144, 4, "nvfp4_mojo_tc")

    def forward(self, x: Tensor, vec: Tensor, cos: Tensor, sin: Tensor) -> Tensor:
        x = x.cast(DType.bfloat16)
        if a.component == "block":
            y = self.block(x, vec.cast(DType.bfloat16), (cos, sin))
        elif a.component == "attn":
            y = self.block(x, (cos, sin))
        else:
            y = self.block(x)
        return y.cast(DType.float32)


ref = np.load(a.ref)
x, vec = ref[a.component + "_in"], ref["vec"]
txt = a.txt
h = w = int(np.sqrt(x.shape[1] - txt))
cos, sin = krea2.rope_tables(txt, h, w)

dev = Accelerator()
weights = load_weights([Path(a.ckpt)])
sd = {}
prefix = "blocks.0." + (a.component + "." if a.component != "block" else "")
for key, value in weights.items():
    if not key.startswith(prefix):
        continue
    value = krea2.prepare_nvfp4_weight(key, value.data())
    sd["block." + key[len(prefix):]] = value

with F.lazy():
    model = Wrap()
    model.to(dev)
run = model.compile(
    TensorType(DType.float32, x.shape, dev),
    TensorType(DType.float32, vec.shape, dev),
    TensorType(DType.float32, cos.shape, dev),
    TensorType(DType.float32, sin.shape, dev),
    weights=sd,
    custom_extensions=[krea2.KERNELS],
)

def gpu(array):
    return Tensor.from_dlpack(np.ascontiguousarray(array, dtype=np.float32)).to(dev)

got = run(gpu(x), gpu(vec), gpu(cos), gpu(sin)).to_numpy()
expected = ref[a.component + "_out"]
cosine = float((got * expected).sum() / (np.linalg.norm(got) * np.linalg.norm(expected)))
print("got", got.mean(), got.std(), "expected", expected.mean(), expected.std())
print(
    f"cosine {cosine:.8f}; rel-L2 {np.linalg.norm(got - expected) / np.linalg.norm(expected):.6g}; "
    f"max|d| {np.abs(got - expected).max():.6g}"
)
np.savez(f"/tmp/krea2-nvfp4-{a.component}-out.npz", got=got, expected=expected)
