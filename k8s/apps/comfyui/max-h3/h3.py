"""MiniMax H3 Image DiT on Modular MAX (module v3 API).

This is a DiT-only port of ComfyUI's comfy/ldm/minimax/model.py. The initial
implementation uses BF16-dequantized W4A8 weights; it does not reproduce
Comfy's Hadamard + dynamic-INT8 activation path. Text encoding and VAE are
outside this package.

The host owns the packed layout, AdaLN curve lookup, partial split-half RoPE
tables and PDD head selection. The graph consumes already-patchified visual
rows in checkpoint order: [reference images | target image]. Text rows are
refined in the graph and prepended to those visual rows.

Spike talos-config-vnb4.
"""
from __future__ import annotations

import math
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
FP32 = DType.float32
KERNELS = Path(__file__).parent / "kernels"
HIDDEN = 5376
HEADS = 56
HEAD_DIM = 128
INNER = HEADS * HEAD_DIM
FFN = 14336
TEXT_DIM = 5120
PATCH_DIM = 24 * 1 * 2 * 2
ROPE_DIM = 96
ROPE_HALF = ROPE_DIM // 2
VISUAL_COND_TIMESTEP = 0.999
PDD_STEPS = 6
PDD_SIGMAS = np.array(
    [1.0, 0.9675236940383911, 0.8875485062599182, 0.75,
     0.5327739119529724, 0.23200887441635132, 0.0],
    dtype=np.float32,
)


class RMSNorm(Module[[Tensor], Tensor]):
    weight: Tensor

    def __init__(self, dim: int, eps: float = 1e-5) -> None:
        self.weight = Tensor.zeros([dim], dtype=BF16)
        self.eps = eps

    def forward(self, x: Tensor) -> Tensor:
        return rms_norm(x, self.weight, self.eps)


class FP32Linear(Module[[Tensor], Tensor]):
    weight: Tensor
    bias: Tensor

    def __init__(self, in_dim: int, out_dim: int) -> None:
        self.weight = Tensor.zeros([out_dim, in_dim], dtype=FP32)
        self.bias = Tensor.zeros([out_dim], dtype=FP32)

    def forward(self, x: Tensor) -> Tensor:
        return x @ self.weight.T + self.bias


class CurveLinear(Module[[Tensor], Tensor]):
    """The checkpoint's FP16-weight/BF16-bias curve projection, run in FP32."""

    weight: Tensor
    bias: Tensor

    def __init__(self, out_dim: int) -> None:
        self.weight = Tensor.zeros([out_dim, 8], dtype=DType.float16)
        self.bias = Tensor.zeros([out_dim], dtype=BF16)

    def forward(self, x: Tensor) -> Tensor:
        # MAX 26.7's GEMV fails for M=1, K=8, N=96768. Eight explicit
        # terms are tiny here and preserve the FP32 AdaLN projection.
        weight = self.weight.cast(FP32)
        out = self.bias.cast(FP32)
        for i in range(8):
            column = F.reshape(weight[:, i], [1, self.weight.shape[0]])
            out = out + x[:, i:i + 1] * column
        return out


def apply_rope(x: Tensor, cos: Tensor, sin: Tensor) -> Tensor:
    """Per-head partial split-half RoPE: rotate 96 of 128 channels.

    x is [1, S, heads, 128], cos/sin are [S, 48]. Pairs are (i, i+48),
    not adjacent pairs; channels 96:128 pass through unchanged.
    """
    seq = x.shape[1]
    cos = F.reshape(cos.cast(x.dtype), [1, seq, 1, ROPE_HALF])
    sin = F.reshape(sin.cast(x.dtype), [1, seq, 1, ROPE_HALF])
    first = x[..., :ROPE_HALF]
    second = x[..., ROPE_HALF:ROPE_DIM]
    rotated = F.concat(
        [first * cos - second * sin, first * sin + second * cos], axis=-1
    )
    return F.concat([rotated, x[..., ROPE_DIM:]], axis=-1)


class W4A8Linear(Module[[Tensor], Tensor]):
    """Canonical packed W4A8 projection with custom Mojo INT8 kernels."""

    weight: Tensor
    weight_s_rel: Tensor
    weight_s_channel: Tensor
    weight_codebook: Tensor

    def __init__(self, in_dim: int, out_dim: int) -> None:
        self.weight = Tensor.zeros([out_dim, in_dim // 2], dtype=DType.int8)
        self.weight_s_rel = Tensor.zeros(
            [out_dim, in_dim // 16], dtype=DType.float8_e4m3fn
        )
        self.weight_s_channel = Tensor.zeros([out_dim], dtype=FP32)
        self.weight_codebook = Tensor.zeros([16], dtype=FP32)

    def forward(self, x: Tensor) -> Tensor:
        m, k, n = x.shape[0], x.shape[1], self.weight.shape[0]
        rotated = F.custom(
            "h3_convrot256", device=x.device, values=[x],
            out_types=[TensorType(BF16, [m, k], x.device)],
        )[0]
        xq, xs = F.custom(
            "h3_int8_quant", device=x.device, values=[rotated],
            out_types=[
                TensorType(DType.int8, [m, k], x.device),
                TensorType(FP32, [m], x.device),
            ],
        )
        acc = F.custom(
            "h3_w4a8_mma", device=x.device,
            values=[xq, self.weight, self.weight_s_rel, self.weight_codebook],
            out_types=[TensorType(DType.int32, [m, n], x.device)],
        )[0]
        out = acc.cast(FP32) * F.reshape(xs, [m, 1]) * self.weight_s_channel
        return out.cast(BF16)


def make_linear(in_dim: int, out_dim: int, quant: str) -> Module:
    if quant == "w4a8":
        return W4A8Linear(in_dim, out_dim)
    if quant == "bf16":
        return Linear(in_dim, out_dim, bias=False)
    raise NotImplementedError(quant)


class Attention(Module[..., Tensor]):
    def __init__(self, quant: str = "bf16") -> None:
        self.heads = HEADS
        self.head_dim = HEAD_DIM
        self.qkv_proj = make_linear(HIDDEN, INNER * 3, quant)
        self.q_norm = RMSNorm(HEAD_DIM)
        self.k_norm = RMSNorm(HEAD_DIM)
        self.out_proj = make_linear(INNER, HIDDEN, quant)

    def forward(self, x: Tensor, rope: tuple[Tensor, Tensor] | None = None) -> Tensor:
        seq = x.shape[0]
        q, k, v = F.chunk(self.qkv_proj(x), 3, axis=-1)
        q = F.reshape(q, [1, seq, HEADS, HEAD_DIM])
        k = F.reshape(k, [1, seq, HEADS, HEAD_DIM])
        v = F.reshape(v, [1, seq, HEADS, HEAD_DIM])
        q, k = self.q_norm(q), self.k_norm(k)
        if rope is not None:
            q, k = apply_rope(q, *rope), apply_rope(k, *rope)
        out = flash_attention_gpu(
            q, k, v, mask_variant=MHAMaskVariant.NULL_MASK,
            scale=math.sqrt(1.0 / HEAD_DIM),
        )
        return self.out_proj(F.reshape(out, [seq, INNER]))


class MLP(Module[[Tensor], Tensor]):
    def __init__(self, quant: str = "bf16") -> None:
        self.fc1 = make_linear(HIDDEN, FFN * 2, quant)
        self.fc2 = make_linear(FFN, HIDDEN, quant)

    def forward(self, x: Tensor) -> Tensor:
        gate, up = F.chunk(self.fc1(x), 2, axis=-1)
        return self.fc2(F.silu(gate) * up)


class AdaLNProj(Module[[Tensor], list[Tensor]]):
    def __init__(self, expand: int, modalities: int) -> None:
        self.expand = expand
        self.modalities = modalities
        self.linear = CurveLinear(expand * HIDDEN * modalities)

    def forward(self, t_emb: Tensor) -> list[Tensor]:
        levels = t_emb.shape[0]
        x = self.linear(t_emb)  # no SiLU for curve-table checkpoints
        x = F.reshape(x, [levels * self.modalities, self.expand * HIDDEN])
        return F.chunk(x, self.expand, axis=-1)


class RefinerBlock(Module[[Tensor], Tensor]):
    def __init__(self) -> None:
        self.norm1 = RMSNorm(HIDDEN)
        self.norm2 = RMSNorm(HIDDEN)
        self.attn = Attention()
        self.mlp = MLP()

    def forward(self, x: Tensor) -> Tensor:
        x = x + self.attn(self.norm1(x))
        return x + self.mlp(self.norm2(x))


class TokenRefiner(Module[[Tensor], Tensor]):
    def __init__(self) -> None:
        self.blocks = ModuleList([RefinerBlock() for _ in range(2)])
        self.final_norm = RMSNorm(HIDDEN)

    def forward(self, x: Tensor) -> Tensor:
        for block in self.blocks:
            x = block(x)
        return self.final_norm(x)


class DiTBlock(Module[..., Tensor]):
    def __init__(self, quant: str = "bf16") -> None:
        self.norm1 = RMSNorm(HIDDEN)
        self.norm2 = RMSNorm(HIDDEN)
        self.attn = Attention(quant)
        self.mlp = MLP(quant)
        self.adaln_proj = AdaLNProj(6, 3)

    def forward(
        self, x: Tensor, t_emb: Tensor, mod_indices: Tensor,
        rope: tuple[Tensor, Tensor],
    ) -> Tensor:
        shift_msa, scale_msa, gate_msa, shift_mlp, scale_mlp, gate_mlp = self.adaln_proj(t_emb)
        shift_msa, scale_msa, gate_msa, shift_mlp, scale_mlp, gate_mlp = [
            F.gather(v, mod_indices, axis=0).cast(BF16)
            for v in (shift_msa, scale_msa, gate_msa, shift_mlp, scale_mlp, gate_mlp)
        ]
        h = self.norm1(x) * (1.0 + scale_msa) + shift_msa
        x = x + self.attn(h, rope) * gate_msa
        h = self.norm2(x) * (1.0 + scale_mlp) + shift_mlp
        return x + self.mlp(h) * gate_mlp


class FinalLayer(Module[..., Tensor]):
    def __init__(self) -> None:
        self.norm = RMSNorm(HIDDEN)
        self.adaln_proj = AdaLNProj(2, 1)

    def forward(
        self, x: Tensor, t_emb: Tensor, target_indices: Tensor,
        pdd_weight: Tensor, pdd_bias: Tensor,
    ) -> Tensor:
        shift, scale = self.adaln_proj(t_emb)
        shift = F.gather(shift, target_indices, axis=0)
        scale = F.gather(scale, target_indices, axis=0)
        h = (self.norm(x) * (1.0 + scale.cast(BF16)) + shift.cast(BF16)).cast(FP32)
        return h @ pdd_weight.T + pdd_bias


class H3ImageDiT(Module[..., Tensor]):
    def __init__(self, layers: int = 2, quant: str = "bf16") -> None:
        self.video_patch_proj = FP32Linear(PATCH_DIM, HIDDEN)
        self.condition_proj = Linear(TEXT_DIM, HIDDEN, bias=True)
        self.token_refiner = TokenRefiner()
        self.blocks = ModuleList([DiTBlock(quant) for _ in range(layers)])
        self.final_layer = FinalLayer()

    def input_types(
        self, device, visual_rows: int, text_rows: int, time_levels: int,
        target_rows: int,
    ) -> tuple[TensorType, ...]:
        seq_rows = text_rows + visual_rows
        return (
            TensorType(FP32, [visual_rows, PATCH_DIM], device),
            TensorType(FP32, [text_rows, TEXT_DIM], device),
            TensorType(FP32, [time_levels, 8], device),
            TensorType(DType.int64, [seq_rows], device),
            TensorType(DType.int64, [target_rows], device),
            TensorType(FP32, [seq_rows, ROPE_HALF], device),
            TensorType(FP32, [seq_rows, ROPE_HALF], device),
            TensorType(FP32, [PATCH_DIM, HIDDEN], device),
            TensorType(FP32, [PATCH_DIM], device),
        )

    def forward(
        self, visual: Tensor, context: Tensor, t_emb: Tensor,
        mod_indices: Tensor, target_indices: Tensor, rope_cos: Tensor,
        rope_sin: Tensor, pdd_weight: Tensor, pdd_bias: Tensor,
    ) -> Tensor:
        visual = self.video_patch_proj(visual).cast(BF16)
        context = self.token_refiner(self.condition_proj(context.cast(BF16)))
        text_rows = context.shape[0]
        x = F.concat([context, visual], axis=0)
        for block in self.blocks:
            x = block(x, t_emb, mod_indices, (rope_cos, rope_sin))
        target = x[text_rows + visual.shape[0] - target_indices.shape[0]:]
        return -self.final_layer(target, t_emb, target_indices, pdd_weight, pdd_bias)


# ---- host-side packed layout, sigma and RoPE ---------------------------

def patchify(x: np.ndarray) -> np.ndarray:
    """[B,24,T,H,W] -> [B*T*(H/2)*(W/2),96], Comfy's (c,pt,ph,pw) order."""
    b, c, t, h, w = x.shape
    if t != 1 or h % 2 or w % 2:
        raise ValueError(f"initial image port expects [B,C,1,evenH,evenW], got {x.shape}")
    x = x.reshape(b, c, t, 1, h // 2, 2, w // 2, 2)
    x = x.transpose(0, 2, 4, 6, 1, 3, 5, 7)
    return np.ascontiguousarray(x.reshape(-1, c * 4), dtype=np.float32)


def unpatchify(rows: np.ndarray, h: int, w: int) -> np.ndarray:
    """One image's [tokens,96] -> [1,24,1,H,W]."""
    x = rows.reshape(1, h // 2, w // 2, 24, 1, 2, 2)
    x = x.transpose(0, 3, 4, 1, 5, 2, 6)
    return np.ascontiguousarray(x.reshape(1, 24, 1, h, w), dtype=np.float32)


def _axis_from_sqrt_area(dim: int, sqrt_area: float) -> np.ndarray:
    ratio = dim / sqrt_area
    n = dim // 2
    return (np.arange(n, dtype=np.float64) * (ratio / n) + (1.0 - ratio) / 2.0) * 32.0


def _frame_grid(h: int, w: int) -> np.ndarray:
    area = math.sqrt(h * w)
    hh, ww = np.meshgrid(
        _axis_from_sqrt_area(h, area), _axis_from_sqrt_area(w, area), indexing="ij"
    )
    return np.stack([hh.reshape(-1), ww.reshape(-1)], axis=-1)


def packed_layout(
    text_rows: int, target_h: int, target_w: int,
    refs: list[tuple[int, int]] | None = None,
) -> tuple[np.ndarray, list[tuple[int, int, str]]]:
    """Return float64 [S,3] positions and contiguous (start,stop,kind) segments."""
    refs = refs or []
    positions = [np.zeros((text_rows, 3), dtype=np.float64)]
    positions[0][:, 0] = np.arange(text_rows, dtype=np.float64)
    segments = [(0, text_rows, "text")]
    row = text_rows
    cursor = float(text_rows)
    for ref_h, ref_w in refs:
        frame = _frame_grid(ref_h, ref_w)
        pos = np.empty((frame.shape[0], 3), dtype=np.float64)
        pos[:, 0], pos[:, 1:] = cursor, frame
        positions.append(pos)
        segments.append((row, row + len(pos), "ref_img"))
        row += len(pos)
        cursor += 1.0
    frame = _frame_grid(target_h, target_w)
    pos = np.empty((frame.shape[0], 3), dtype=np.float64)
    pos[:, 0], pos[:, 1:] = cursor, frame
    positions.append(pos)
    segments.append((row, row + len(pos), "video"))
    return np.concatenate(positions), segments


def rope_tables(position_ids: np.ndarray, inv_freq: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    per_axis = position_ids.astype(np.float32)[..., None] * inv_freq.reshape(1, 1, -1)
    half = np.concatenate([per_axis[:, 0], per_axis[:, 1], per_axis[:, 2]], axis=-1)
    return np.cos(half).astype(np.float32), np.sin(half).astype(np.float32)


def adaln_curve(table: np.ndarray, values: list[float]) -> np.ndarray:
    t = np.asarray(values, dtype=np.float32)
    pos = np.clip(t, 0.0, 1.0) * (table.shape[0] - 1)
    i0 = np.minimum(np.floor(pos).astype(np.int64), table.shape[0] - 2)
    frac = (pos - i0)[:, None]
    return (table[i0] * (1.0 - frac) + table[i0 + 1] * frac).astype(np.float32)


def pdd_head(
    weight: np.ndarray, bias: np.ndarray, sigma: float,
    sample_sigmas: np.ndarray, grid: np.ndarray = PDD_SIGMAS,
) -> tuple[np.ndarray, np.ndarray]:
    """Select/blend the PDD head bank exactly as Comfy's FinalLayer does."""
    n = len(grid) - 1
    i = int(np.argmin(np.abs(sample_sigmas - sigma)))
    sigma_next = float(sample_sigmas[min(i + 1, len(sample_sigmas) - 1)])
    start, stop = (min(range(n + 1), key=lambda k: abs(float(grid[k]) - s))
                   for s in (sigma, sigma_next))
    start = min(start, n - 1)
    stop = max(stop, start + 1)
    dt = np.asarray([grid[k] - grid[k + 1] for k in range(start, stop)], dtype=np.float32)
    weights = dt / np.sum(dt, dtype=np.float32)
    rows = weight.reshape(n, PATCH_DIM, HIDDEN)
    brows = bias.reshape(n, PATCH_DIM)
    first = max(start, 1)
    selected = weights[first - start:]
    return (
        np.ascontiguousarray(rows[0] + np.einsum("n,noi->oi", selected, rows[first:stop])),
        np.ascontiguousarray(brows[0] + np.einsum("n,no->o", selected, brows[first:stop])),
    )


def prepare_inputs(
    x: np.ndarray, context: np.ndarray, sigma: float, sample_sigmas: np.ndarray,
    adaln_table: np.ndarray, inv_freq: np.ndarray, pdd_weight: np.ndarray,
    pdd_bias: np.ndarray, refs: list[np.ndarray] | None = None,
    text_token_tags: np.ndarray | None = None,
) -> dict[str, np.ndarray]:
    """Build all dynamic graph inputs for one Comfy-compatible forward."""
    refs = refs or []
    if context.ndim == 3:
        if context.shape[0] != 1:
            raise ValueError("H3 supports batch size 1")
        context = context[0]
    text_rows = context.shape[0]
    target_h, target_w = x.shape[-2:]
    ref_shapes = [(r.shape[-2], r.shape[-1]) for r in refs]
    positions, segments = packed_layout(text_rows, target_h, target_w, ref_shapes)
    t = 1.0 - sigma
    ref_t = max(t, VISUAL_COND_TIMESTEP)
    unique_t = sorted({t} | ({ref_t} if refs else set()))
    t_row = {value: i for i, value in enumerate(unique_t)}
    mod_indices = np.empty(positions.shape[0], dtype=np.int64)
    for start, stop, kind in segments:
        if kind == "text":
            tags = (np.ones(stop - start, dtype=np.int64) if text_token_tags is None
                    else np.asarray(text_token_tags, dtype=np.int64))
            if tags.shape != (stop - start,):
                raise ValueError(f"text_token_tags must have {(stop - start,)}, got {tags.shape}")
            mod_indices[start:stop] = t_row[t] * 3 + tags
        else:
            level = ref_t if kind == "ref_img" else t
            mod_indices[start:stop] = t_row[level] * 3  # visual tag = 0
    visual = np.concatenate([*(patchify(r) for r in refs), patchify(x)])
    head_w, head_b = pdd_head(pdd_weight, pdd_bias, sigma, sample_sigmas)
    cos, sin = rope_tables(positions, inv_freq)
    return {
        "visual": visual,
        "context": np.ascontiguousarray(context, dtype=np.float32),
        "t_emb": adaln_curve(adaln_table, unique_t),
        "mod_indices": mod_indices,
        "target_indices": np.full(x.shape[-1] // 2 * (x.shape[-2] // 2), t_row[t], dtype=np.int64),
        "rope_cos": cos,
        "rope_sin": sin,
        "pdd_weight": head_w,
        "pdd_bias": head_b,
        "position_ids": positions,
        "unique_t": np.asarray(unique_t, dtype=np.float32),
    }
