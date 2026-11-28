"""Krea 2 (K2) single-stream DiT on Modular MAX (module v3 API).

A port of ComfyUI's comfy/ldm/krea2/model.py (v0.45.5), text-to-image
path only (no reference latents). Parameter names follow the Comfy-Org
single-file checkpoints so a state dict maps straight on, except the
nn.Sequential members, which `remap_keys` renames.

Everything the graph does not need to learn is computed on the host
and passed in (see `patchify`, `timestep_embedding` and `rope_tables`):
the patchified latent, timestep embedding and RoPE tables. Spike
talos-config-j1kd.
"""
from __future__ import annotations

import functools
import math
import operator
from pathlib import Path

import numpy as np
from max.dtype import DType
from max.experimental import functional as F
from max.experimental.nn import Linear, Module
from max.experimental.nn.norm.rms_norm import rms_norm
from max.experimental.nn.sequential import ModuleList
from max.experimental.tensor import Tensor, TensorType
from max.nn.attention.mask_config import MHAMaskVariant
from max.nn.kernels import flash_attention_gpu as _flash_attention_gpu

flash_attention_gpu = F.functional(_flash_attention_gpu)

BF16 = DType.bfloat16
KERNELS = Path(__file__).parent / "kernels"
HEAD_DIM = 128
SEQUENTIAL = {  # checkpoint (nn.Sequential index) -> attribute name here
    "tmlp.0.": "tmlp_in.", "tmlp.2.": "tmlp_out.", "tproj.1.": "tproj.",
    "txtmlp.0.": "txtmlp_norm.", "txtmlp.1.": "txtmlp_in.", "txtmlp.3.": "txtmlp_out.",
}


def remap_keys(sd: dict) -> dict:
    out = {}
    for k, v in sd.items():
        for a, b in SEQUENTIAL.items():
            if k.startswith(a):
                k = b + k[len(a):]
                break
        out[k] = v
    return out


class RMSNorm1p(Module[[Tensor], Tensor]):
    """RMSNorm whose stored `scale` is zero-centred: weight = 1 + scale."""

    scale: Tensor

    def __init__(self, dim: int, eps: float = 1e-5) -> None:
        self.scale = Tensor.zeros([dim], dtype=BF16)
        self.eps = eps

    def forward(self, x: Tensor) -> Tensor:
        return rms_norm(x, self.scale, self.eps, weight_offset=1.0, multiply_before_cast=True)


class ScaledFP8Linear(Module[[Tensor], Tensor]):
    """Bias-free Linear whose weight is stored as FP8 with one scalar scale
    (Comfy `fp8_scaled`). The weight stays FP8 in memory and is widened to
    bf16 for the matmul — the same thing ComfyUI does for these layers."""

    weight: Tensor
    weight_scale: Tensor

    def __init__(self, in_dim: int, out_dim: int) -> None:
        self.weight = Tensor.zeros([out_dim, in_dim], dtype=DType.float8_e4m3fn)
        self.weight_scale = Tensor.zeros([], dtype=DType.float32)

    def forward(self, x: Tensor) -> Tensor:
        w = self.weight.cast(BF16) * self.weight_scale.cast(BF16)
        return x @ w.T


def make_linear(in_dim: int, out_dim: int, quant: str) -> Module:
    if quant == "fp8_scaled":
        return ScaledFP8Linear(in_dim, out_dim)
    if quant == "fp8_mojo":
        return MojoScaledFP8Linear(in_dim, out_dim)
    if quant == "fp8_mojo_fused":
        return MojoFusedFP8Linear(in_dim, out_dim)
    if quant == "fp8_mojo_tc":
        return MojoTensorCoreFP8Linear(in_dim, out_dim)
    if quant == "bf16":
        return Linear(in_dim, out_dim, bias=False)
    raise NotImplementedError(quant)


class MojoScaledFP8Linear(Module[[Tensor], Tensor]):
    """ScaledFP8Linear with dequantization implemented as a Mojo custom op."""

    weight: Tensor
    weight_scale: Tensor

    def __init__(self, in_dim: int, out_dim: int) -> None:
        self.weight = Tensor.zeros([out_dim, in_dim], dtype=DType.float8_e4m3fn)
        self.weight_scale = Tensor.zeros([], dtype=DType.float32)

    def forward(self, x: Tensor) -> Tensor:
        w = F.custom(
            "krea2_fp8_dequant",
            device=self.weight.device,
            values=[self.weight, F.reshape(self.weight_scale, [1])],
            out_types=[TensorType(BF16, self.weight.shape, self.weight.device)],
        )[0]
        return x @ w.T


class MojoFusedFP8Linear(Module[[Tensor], Tensor]):
    """Experimental Mojo tiled GEMM; no full BF16 weight is materialized.

    The MAX 26.7 kernel currently requires M, N and K divisible by 32 and
    uses scalar FP32 accumulation rather than tensor cores.
    """

    weight: Tensor
    weight_scale: Tensor

    def __init__(self, in_dim: int, out_dim: int) -> None:
        self.weight = Tensor.zeros([out_dim, in_dim], dtype=DType.float8_e4m3fn)
        self.weight_scale = Tensor.zeros([], dtype=DType.float32)

    def forward(self, x: Tensor) -> Tensor:
        lead = list(x.shape[:-1])
        m = functools.reduce(operator.mul, lead)
        x_2d = F.reshape(x, [m, x.shape[-1]]).cast(BF16)
        y = F.custom(
            "krea2_fp8_linear",
            device=x.device,
            values=[x_2d, self.weight, F.reshape(self.weight_scale, [1])],
            out_types=[TensorType(BF16, [m, self.weight.shape[0]], x.device)],
        )[0]
        return F.reshape(y, [*lead, self.weight.shape[0]])


class MojoTensorCoreFP8Linear(MojoFusedFP8Linear):
    """Fused Mojo linear using BF16 tensor cores with FP32 accumulation."""

    def forward(self, x: Tensor) -> Tensor:
        lead = list(x.shape[:-1])
        m = functools.reduce(operator.mul, lead)
        x_2d = F.reshape(x, [m, x.shape[-1]]).cast(BF16)
        y = F.custom(
            "krea2_fp8_linear_tc",
            device=x.device,
            values=[x_2d, self.weight, F.reshape(self.weight_scale, [1])],
            out_types=[TensorType(DType.float32, [m, self.weight.shape[0]], x.device)],
        )[0]
        y = y.cast(BF16)
        return F.reshape(y, [*lead, self.weight.shape[0]])


class SwiGLU(Module[[Tensor], Tensor]):
    def __init__(self, dim: int, multiplier: int, quant: str, multiple: int = 128) -> None:
        mlp = int(2 * dim / 3) * multiplier
        mlp = multiple * ((mlp + multiple - 1) // multiple)
        self.gate = make_linear(dim, mlp, quant)
        self.up = make_linear(dim, mlp, quant)
        self.down = make_linear(mlp, dim, quant)

    def forward(self, x: Tensor) -> Tensor:
        return self.down(F.silu(self.gate(x)) * self.up(x))


class QKNorm(Module[..., tuple[Tensor, Tensor]]):
    def __init__(self, dim: int) -> None:
        self.qnorm = RMSNorm1p(dim)
        self.knorm = RMSNorm1p(dim)

    def forward(self, q: Tensor, k: Tensor) -> tuple[Tensor, Tensor]:
        return self.qnorm(q), self.knorm(k)


def apply_rope(x: Tensor, cos: Tensor, sin: Tensor) -> Tensor:
    """x: [B, L, H, D]; cos/sin: [L, D/2] float32. Pairs are (x[2i], x[2i+1])."""
    b, l, h, d = x.shape
    dtype = x.dtype
    x = F.reshape(x.cast(DType.float32), [b, l, h, d // 2, 2])
    x0, x1 = x[..., 0], x[..., 1]
    cos, sin = F.unsqueeze(F.unsqueeze(cos, 0), 2), F.unsqueeze(F.unsqueeze(sin, 0), 2)
    out = F.stack([cos * x0 - sin * x1, sin * x0 + cos * x1], axis=-1)
    return F.reshape(out, [b, l, h, d]).cast(dtype)


class Attention(Module[..., Tensor]):
    """GQA + per-head QK-norm + RoPE + sigmoid-gated output."""

    def __init__(self, dim: int, heads: int, kvheads: int, quant: str) -> None:
        self.heads, self.kvheads = heads, kvheads
        self.wq = make_linear(dim, HEAD_DIM * heads, quant)
        self.wk = make_linear(dim, HEAD_DIM * kvheads, quant)
        self.wv = make_linear(dim, HEAD_DIM * kvheads, quant)
        self.gate = make_linear(dim, dim, quant)
        self.qknorm = QKNorm(HEAD_DIM)
        self.wo = make_linear(dim, dim, quant)

    def forward(self, x: Tensor, rope: tuple[Tensor, Tensor] | None = None) -> Tensor:
        b, l, dim = x.shape
        q = F.reshape(self.wq(x), [b, l, self.heads, HEAD_DIM])
        k = F.reshape(self.wk(x), [b, l, self.kvheads, HEAD_DIM])
        v = F.reshape(self.wv(x), [b, l, self.kvheads, HEAD_DIM])
        gate = self.gate(x)
        q, k = self.qknorm(q, k)
        if rope is not None:
            q, k = apply_rope(q, *rope), apply_rope(k, *rope)
        if self.kvheads != self.heads:
            rep = self.heads // self.kvheads
            k = F.repeat_interleave(k, rep, axis=2)
            v = F.repeat_interleave(v, rep, axis=2)
        out = flash_attention_gpu(q, k, v, mask_variant=MHAMaskVariant.NULL_MASK,
                                  scale=math.sqrt(1.0 / HEAD_DIM))
        out = F.reshape(out, [b, l, dim])
        return self.wo(out * F.sigmoid(gate))


class TextFusionBlock(Module[[Tensor], Tensor]):
    def __init__(self, dim: int, heads: int, kvheads: int, multiplier: int, quant: str) -> None:
        self.prenorm = RMSNorm1p(dim)
        self.postnorm = RMSNorm1p(dim)
        self.attn = Attention(dim, heads, kvheads, quant)
        self.mlp = SwiGLU(dim, multiplier, quant)

    def forward(self, x: Tensor) -> Tensor:
        x = x + self.attn(self.prenorm(x))
        return x + self.mlp(self.postnorm(x))


class TextFusionTransformer(Module[[Tensor], Tensor]):
    """[B, seq, layers, dim] hidden-state stack -> [B, seq, dim]."""

    def __init__(self, layers: int, dim: int, heads: int, kvheads: int, multiplier: int, quant: str) -> None:
        self.layerwise_blocks = ModuleList(
            [TextFusionBlock(dim, heads, kvheads, multiplier, quant) for _ in range(2)])
        self.projector = Linear(layers, 1, bias=False)
        self.refiner_blocks = ModuleList(
            [TextFusionBlock(dim, heads, kvheads, multiplier, quant) for _ in range(2)])

    def forward(self, x: Tensor) -> Tensor:
        b, l, n, d = x.shape
        x = F.reshape(x, [b * l, n, d])  # attend across the tapped layers, per token
        for blk in self.layerwise_blocks:
            x = blk(x)
        x = F.reshape(x, [b, l, n, d]).permute([0, 1, 3, 2])
        x = F.reshape(self.projector(x), [b, l, d])
        for blk in self.refiner_blocks:
            x = blk(x)
        return x


class Modulation6(Module[[Tensor], list[Tensor]]):
    lin: Tensor

    def __init__(self, dim: int) -> None:
        self.lin = Tensor.zeros([6 * dim], dtype=BF16)

    def forward(self, vec: Tensor) -> list[Tensor]:
        return F.chunk(vec + self.lin, 6, axis=-1)


class SingleStreamBlock(Module[..., Tensor]):
    def __init__(self, dim: int, heads: int, kvheads: int, multiplier: int, quant: str) -> None:
        self.mod = Modulation6(dim)
        self.prenorm = RMSNorm1p(dim)
        self.postnorm = RMSNorm1p(dim)
        self.attn = Attention(dim, heads, kvheads, quant)
        self.mlp = SwiGLU(dim, multiplier, quant)

    def forward(self, x: Tensor, vec: Tensor, rope: tuple[Tensor, Tensor]) -> Tensor:
        prescale, preshift, pregate, postscale, postshift, postgate = self.mod(vec)
        x = x + pregate * self.attn((1 + prescale) * self.prenorm(x) + preshift, rope)
        return x + postgate * self.mlp((1 + postscale) * self.postnorm(x) + postshift)


class LastModulation(Module[[Tensor], tuple[Tensor, Tensor]]):
    lin: Tensor

    def __init__(self, dim: int) -> None:
        self.lin = Tensor.zeros([2, dim], dtype=BF16)

    def forward(self, t: Tensor) -> tuple[Tensor, Tensor]:
        out = t + F.unsqueeze(self.lin, 0)  # [B, 1, dim] + [1, 2, dim]
        return out[:, 0:1, :], out[:, 1:2, :]


class LastLayer(Module[..., Tensor]):
    def __init__(self, dim: int, out_dim: int) -> None:
        self.norm = RMSNorm1p(dim)
        self.linear = Linear(dim, out_dim, bias=True)
        self.modulation = LastModulation(dim)

    def forward(self, x: Tensor, t: Tensor) -> Tensor:
        scale, shift = self.modulation(t)
        return self.linear((1 + scale) * self.norm(x) + shift)


class Krea2DiT(Module[..., Tensor]):
    def __init__(self, features: int = 6144, tdim: int = 256, txtdim: int = 2560, heads: int = 48,
                 kvheads: int = 12, multiplier: int = 4, layers: int = 28, patch: int = 2, channels: int = 16,
                 txtlayers: int = 12, txtheads: int = 20, txtkvheads: int = 20, quant: str = "fp8_scaled") -> None:
        self.features, self.tdim, self.txtdim, self.txtlayers = features, tdim, txtdim, txtlayers
        self.in_dim = channels * patch * patch
        self.first = Linear(self.in_dim, features, bias=True)
        self.blocks = ModuleList(
            [SingleStreamBlock(features, heads, kvheads, multiplier, quant) for _ in range(layers)])
        self.tmlp_in = Linear(tdim, features)
        self.tmlp_out = Linear(features, features)
        self.txtfusion = TextFusionTransformer(txtlayers, txtdim, txtheads, txtkvheads, multiplier, quant)
        self.txtmlp_norm = RMSNorm1p(txtdim)
        self.txtmlp_in = Linear(txtdim, features)
        self.txtmlp_out = Linear(features, features)
        self.last = LastLayer(features, self.in_dim)
        self.tproj = Linear(features, features * 6)

    def input_types(self, device) -> tuple[TensorType, ...]:
        return (
            TensorType(DType.float32, ["batch", "img_len", self.in_dim], device),  # patchified latent
            TensorType(DType.float32, ["batch", "txt_len", self.txtlayers, self.txtdim], device),  # hidden states
            TensorType(DType.float32, ["batch", self.tdim], device),                # sinusoid timestep embedding
            TensorType(DType.float32, ["seq_len", HEAD_DIM // 2], device),          # rope cos, text then image
            TensorType(DType.float32, ["seq_len", HEAD_DIM // 2], device),          # rope sin
        )

    def forward(self, img: Tensor, context: Tensor, temb: Tensor, rope_cos: Tensor, rope_sin: Tensor) -> Tensor:
        # float32 at the graph boundary, bf16 inside: a cast outside a compiled
        # graph goes through MAX's eager compile pool.
        img, context, temb = img.cast(BF16), context.cast(BF16), temb.cast(BF16)
        img = self.first(img)
        t = self.tmlp_out(F.gelu(self.tmlp_in(F.unsqueeze(temb, 1)), approximate="tanh"))  # [B, 1, features]
        tvec = self.tproj(F.gelu(t, approximate="tanh"))
        context = self.txtfusion(context)
        context = self.txtmlp_out(F.gelu(self.txtmlp_in(self.txtmlp_norm(context)), approximate="tanh"))
        txt_len = context.shape[1]
        x = F.concat([context, img], axis=1)
        x = F.rebind(x, [x.shape[0], rope_cos.shape[0], self.features])
        for blk in self.blocks:
            x = blk(x, tvec, (rope_cos, rope_sin))
        return self.last(x, t)[:, txt_len:, :].cast(DType.float32)


# ---- host side ---------------------------------------------------------

def timestep_embedding(t: np.ndarray, dim: int = 256, max_period: float = 10000.0,
                       time_factor: float = 1000.0) -> np.ndarray:
    half = dim // 2
    freqs = np.exp(-math.log(max_period) * np.arange(half, dtype=np.float32) / half)
    args = (time_factor * t.astype(np.float32))[:, None] * freqs[None]
    return np.concatenate([np.cos(args), np.sin(args)], axis=-1).astype(np.float32)


def rope_tables(txt_len: int, h: int, w: int, theta: float = 1000.0) -> tuple[np.ndarray, np.ndarray]:
    """cos/sin [txt_len + h*w, HEAD_DIM/2]: text at (0,0,0), image at (0, y, x)."""
    axes = [HEAD_DIM - 12 * (HEAD_DIM // 16), 6 * (HEAD_DIM // 16), 6 * (HEAD_DIM // 16)]
    pos = np.zeros((txt_len + h * w, 3), dtype=np.float32)
    ys, xs = np.meshgrid(np.arange(h), np.arange(w), indexing="ij")
    pos[txt_len:, 1], pos[txt_len:, 2] = ys.reshape(-1), xs.reshape(-1)
    ang = []
    for i, d in enumerate(axes):
        omega = 1.0 / (theta ** np.linspace(0, (d - 2) / d, d // 2, dtype=np.float64))
        ang.append(pos[:, i:i + 1].astype(np.float64) * omega[None])
    ang = np.concatenate(ang, axis=-1)
    return np.cos(ang).astype(np.float32), np.sin(ang).astype(np.float32)


def patchify(x: np.ndarray, p: int = 2) -> np.ndarray:
    """[B, C, H, W] -> [B, (H/p)(W/p), C*p*p], feature order (c, ph, pw)."""
    b, c, h, w = x.shape
    x = x.reshape(b, c, h // p, p, w // p, p).transpose(0, 2, 4, 1, 3, 5)
    return x.reshape(b, (h // p) * (w // p), c * p * p)


def unpatchify(x: np.ndarray, h: int, w: int, p: int = 2) -> np.ndarray:
    b, _, f = x.shape
    c = f // (p * p)
    x = x.reshape(b, h // p, w // p, c, p, p).transpose(0, 3, 1, 4, 2, 5)
    return x.reshape(b, c, h, w)
