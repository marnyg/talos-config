# MiniMax H3 W4A8 primitives for MAX 26.7.
# Canonical packed weights are low-nibble-first [N,K/2], with FP8 group scales.

from std.math import ceildiv, round
from std.math.uutils import udivmod
from std.sys import _RegisterPackType
from std.sys._assembly import inlined_assembly
from std.memory import bitcast

import extensibility
from extensibility import InputTensor, OutputTensor, foreach
from layout import TensorLayout, TileTensor, row_major, stack_allocation
from layout.tile_io import copy_dram_to_sram
from max.gpu import WARP_SIZE, block_idx, lane_id, thread_idx, warp_id
from max.gpu.host import DeviceContext
from max.gpu.sync import barrier
from std.utils.coord import Coord


@extensibility.register("h3_w4a8_dequant")
struct H3W4A8Dequant:
    """Decode packed codes through codebook/group scales onto the INT8 grid."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.int8, rank=2, ...],
        weight: InputTensor[dtype=DType.int8, rank=2, ...],
        scales: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        codebook: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"

        @inline(.always)
        def dequant[width: Int](idx: Coord) {var} -> SIMD[output.dtype, width]:
            var result = SIMD[output.dtype, width]()
            for lane in range(width):
                var row = Int(idx[0])
                var col = Int(idx[1]) + lane
                var packed = weight.load[1](Coord(row, col // 2))[0].cast[DType.uint8]()
                var code = (packed.cast[DType.int32]() >> Int32(4 * (col & 1))) & 0xF
                var value = codebook.load[1](Coord(code))[0]
                var group_scale = scales.load[1](Coord(row, col // 16))[0].cast[DType.float32]()
                var decoded = round(value * group_scale)
                decoded = min(max(decoded, Float32(-127)), Float32(127))
                result[lane] = decoded.cast[DType.int8]()
            return result

        foreach[target=target](dequant, output, ctx)


def convrot256_kernel[
    a_layout: TensorLayout,
    c_layout: TensorLayout,
    out_dtype: DType,
](
    a: TileTensor[DType.bfloat16, a_layout, MutAnyOrigin],
    c: TileTensor[mut=True, out_dtype, c_layout, MutAnyOrigin],
):
    """One 64-thread block per (row,256-wide group), regular H4 ConvRot."""
    var row = Int(block_idx.y)
    var base = Int(block_idx.x) * 256
    var lane = Int(thread_idx.x)
    var x = stack_allocation[DType.float32, address_space=.SHARED](row_major[256]())
    var y = stack_allocation[DType.float32, address_space=.SHARED](row_major[256]()
    )
    for i in range(lane, 256, 64):
        x[i] = a[row, base + i].cast[DType.float32]()
    barrier()

    var stride = 1
    while stride < 256:
        # 64 independent H4 butterflies at every stage.
        var index = lane
        var j = index % stride
        var start = (index // stride) * (stride * 4) + j
        var x0 = x[start]
        var x1 = x[start + stride]
        var x2 = x[start + 2 * stride]
        var x3 = x[start + 3 * stride]
        y[start] = x0 + x1 + x2 - x3
        y[start + stride] = x0 + x1 - x2 + x3
        y[start + 2 * stride] = x0 - x1 + x2 + x3
        y[start + 3 * stride] = -x0 + x1 + x2 + x3
        barrier()
        for i in range(lane, 256, 64):
            x[i] = y[i]
        barrier()
        stride *= 4

    for i in range(lane, 256, 64):
        c[row, base + i] = (x[i] / Float32(16)).cast[out_dtype]()


@extensibility.register("h3_convrot256")
struct H3ConvRot256:
    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.bfloat16, rank=2, ...],
        x: InputTensor[dtype=DType.bfloat16, rank=2, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var K = Int(x_tt.dim[1]())
        if K % 256 != 0:
            raise Error("h3_convrot256 requires K divisible by 256")
        comptime kernel = convrot256_kernel[
            type_of(x_tt).LayoutType,
            type_of(out_tt).LayoutType,
            DType.bfloat16,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt, out_tt, grid_dim=(K // 256, M), block_dim=64
        )


@extensibility.register("h3_convrot256_fp32")
struct H3ConvRot256FP32:
    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.float32, rank=2, ...],
        x: InputTensor[dtype=DType.bfloat16, rank=2, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var K = Int(x_tt.dim[1]())
        if K % 256 != 0:
            raise Error("h3_convrot256_fp32 requires K divisible by 256")
        comptime kernel = convrot256_kernel[
            type_of(x_tt).LayoutType,
            type_of(out_tt).LayoutType,
            DType.float32,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt, out_tt, grid_dim=(K // 256, M), block_dim=64
        )


def int8_quant_kernel[
    a_layout: TensorLayout,
    q_layout: TensorLayout,
    s_layout: TensorLayout,
    in_dtype: DType,
    NUM_THREADS: Int,
](
    a: TileTensor[in_dtype, a_layout, MutAnyOrigin],
    q: TileTensor[mut=True, DType.int8, q_layout, MutAnyOrigin],
    scales: TileTensor[mut=True, DType.float32, s_layout, MutAnyOrigin],
):
    var row = Int(block_idx.x)
    var lane = Int(thread_idx.x)
    var K = Int(a.dim[1]())
    var local_max = Float32(0)
    for k in range(lane, K, NUM_THREADS):
        local_max = max(local_max, abs(a[row, k].cast[DType.float32]()))
    var partial = stack_allocation[DType.float32, address_space=.SHARED](
        row_major[NUM_THREADS]()
    )
    partial[lane] = local_max
    barrier()
    var offset = NUM_THREADS // 2
    while offset > 0:
        if lane < offset:
            partial[lane] = max(partial[lane], partial[lane + offset])
        barrier()
        offset //= 2
    var amax = max(partial[0], Float32(1e-30))
    if lane == 0:
        scales[row] = amax / Float32(127)
    # Comfy stores FP32 scales but rounds the scale to the activation dtype
    # before division. An FP32 reciprocal changes ~7% of INT8 codes.
    var math_scale = (amax / Float32(127)).cast[in_dtype]()
    for k in range(lane, K, NUM_THREADS):
        var scaled = (
            a[row, k].cast[DType.float32]() / math_scale.cast[DType.float32]()
        ).cast[in_dtype]()
        var value = round(scaled.cast[DType.float32]())
        # Comfy's activation quantizer uses the full signed INT8 range.
        value = min(max(value, Float32(-128)), Float32(127))
        q[row, k] = value.cast[DType.int8]()


@extensibility.register("h3_int8_quant")
struct H3Int8Quant:
    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.int8, rank=2, ...],
        scales: OutputTensor[dtype=DType.float32, rank=1, ...],
        x: InputTensor[dtype=DType.bfloat16, rank=2, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var scales_tt = scales.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        comptime NUM_THREADS = 256
        comptime kernel = int8_quant_kernel[
            type_of(x_tt).LayoutType,
            type_of(out_tt).LayoutType,
            type_of(scales_tt).LayoutType,
            DType.bfloat16,
            NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt, out_tt, scales_tt, grid_dim=M, block_dim=NUM_THREADS
        )


@extensibility.register("h3_int8_quant_fp32")
struct H3Int8QuantFP32:
    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.int8, rank=2, ...],
        scales: OutputTensor[dtype=DType.float32, rank=1, ...],
        x: InputTensor[dtype=DType.float32, rank=2, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var scales_tt = scales.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        comptime NUM_THREADS = 256
        comptime kernel = int8_quant_kernel[
            type_of(x_tt).LayoutType,
            type_of(out_tt).LayoutType,
            type_of(scales_tt).LayoutType,
            DType.float32,
            NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt, out_tt, scales_tt, grid_dim=M, block_dim=NUM_THREADS
        )


def int8_mma_kernel[
    a_layout: TensorLayout,
    w_layout: TensorLayout,
    c_layout: TensorLayout,
    BM: Int,
    BN: Int,
    BK: Int,
    WM: Int,
    WN: Int,
    NUM_THREADS: Int,
](
    A: TileTensor[DType.int8, a_layout, MutAnyOrigin],
    W: TileTensor[DType.int8, w_layout, MutAnyOrigin],
    C: TileTensor[mut=True, DType.int32, c_layout, MutAnyOrigin],
):
    comptime MMA_M = 16
    comptime MMA_N = 8
    comptime MMA_K = 32
    comptime assert WM % MMA_M == 0 and WN % MMA_N == 0 and BK == MMA_K

    var warp_y, warp_x = udivmod(warp_id(), BN // WN)
    var C_block = C.tile[BM, BN](block_idx.y, block_idx.x)
    var A_smem = stack_allocation[DType.int8, address_space=.SHARED](
        row_major[BM, BK]()
    )
    var W_smem = stack_allocation[DType.int8, address_space=.SHARED](
        row_major[BN, BK]()
    )
    var c_reg = stack_allocation[DType.int32, address_space=.LOCAL](
        row_major[WM // MMA_M, (WN * 4) // MMA_N]()
    ).fill(0)
    var c_smem = stack_allocation[DType.int32, address_space=.SHARED](
        row_major[BM, BN]()
    )
    var warp_region = c_smem.tile[WM, WN](warp_y, warp_x)

    for k_i in range(Int(A.dim[1]()) // BK):
        var A_tile = A.tile[BM, BK](block_idx.y, k_i)
        var W_tile = W.tile[BN, BK](block_idx.x, k_i)
        copy_dram_to_sram[thread_layout=row_major[2, 32]()](A_smem, A_tile)
        copy_dram_to_sram[thread_layout=row_major[2, 32]()](W_smem, W_tile)
        barrier()

        var A_warp = A_smem.tile[WM, BK](warp_y, 0)
        var W_warp = W_smem.tile[WN, BK](warp_x, 0)
        comptime for mma_m in range(WM // MMA_M):
            comptime for mma_n in range(WN // MMA_N):
                var out_reg = c_reg.tile[1, 4](mma_m, mma_n)
                var a_reg = stack_allocation[DType.int8, address_space=.LOCAL](
                    row_major[1, 16]()
                )
                var b_reg = stack_allocation[DType.int8, address_space=.LOCAL](
                    row_major[8, 1]()
                )
                # Standard m16n8k32 operand lane mappings, four INT8 values
                # per 32-bit register. MAX's typed loaders exclude INT8.
                comptime for register in range(16):
                    var row = lane_id() // 4 + 8 * ((register // 4) % 2)
                    var k = (lane_id() % 4) * 4 + 16 * ((register // 4) // 2) + register % 4
                    a_reg[0, register] = A_warp.tile[MMA_M, MMA_K](mma_m, 0)[row, k]
                comptime for register in range(8):
                    var col = lane_id() // 4
                    var k = (lane_id() % 4) * 4 + 16 * (register // 4) + register % 4
                    b_reg[register, 0] = W_warp.tile[MMA_N, MMA_K](mma_n, 0)[col, k]
                # MAX 26.7 has no NVIDIA signed-INT8 mma lowering. Emit the
                # hardware instruction directly with packed 32-bit operands.
                var a_packed = a_reg.vectorize[1, 16]().load((0, 0))
                var b_packed = b_reg.vectorize[8, 1]().load((0, 0))
                var c_packed = out_reg.vectorize[1, 4]().load((0, 0))
                var a_words = bitcast[DType.uint32, 4](a_packed)
                var b_words = bitcast[DType.uint32, 2](b_packed)
                var result = inlined_assembly[
                    (
                        "mma.sync.aligned.m16n8k32.row.col.s32.s8.s8.s32 "
                        "{$0, $1, $2, $3}, {$4, $5, $6, $7}, {$8, $9}, "
                        "{$10, $11, $12, $13};"
                    ),
                    _RegisterPackType[Int32, Int32, Int32, Int32],
                    constraints="=r,=r,=r,=r,r,r,r,r,r,r,r,r,r,r",
                ](
                    a_words[0], a_words[1], a_words[2], a_words[3],
                    b_words[0], b_words[1],
                    c_packed[0], c_packed[1], c_packed[2], c_packed[3],
                )
                out_reg.vectorize[1, 4]().store(
                    (0, 0), SIMD[DType.int32, 4](result[0], result[1], result[2], result[3])
                )
        barrier()

    comptime for mma_m in range(WM // MMA_M):
        comptime for mma_n in range(WN // MMA_N):
            var C_tile = warp_region.tile[MMA_M, MMA_N](mma_m, mma_n)
            var fragment = c_reg.tile[1, 4](mma_m, mma_n)
            # store_d is artificially restricted to FP32/FP64 in MAX 26.7.
            # INT8 uses the same four-register NVIDIA accumulator mapping.
            comptime for register in range(4):
                var row = lane_id() // 4 + (register // 2) * 8
                var col = (lane_id() % 4) * 2 + register % 2
                C_tile[row, col] = fragment[0, register]
    barrier()
    for flat in range(Int(thread_idx.x), BM * BN, NUM_THREADS):
        var i, j = udivmod(flat, BN)
        C_block[i, j] = c_smem[i, j]


def w4a8_mma_kernel[
    a_layout: TensorLayout,
    w_layout: TensorLayout,
    s_layout: TensorLayout,
    cb_layout: TensorLayout,
    xs_layout: TensorLayout,
    sc_layout: TensorLayout,
    c_layout: TensorLayout,
    out_dtype: DType,
    scaled: Bool,
    BM: Int,
    BN: Int,
    BK: Int,
    WM: Int,
    WN: Int,
    NUM_THREADS: Int,
](
    A: TileTensor[DType.int8, a_layout, MutAnyOrigin],
    W: TileTensor[DType.int8, w_layout, MutAnyOrigin],
    S: TileTensor[DType.float8_e4m3fn, s_layout, MutAnyOrigin],
    CB: TileTensor[DType.float32, cb_layout, MutAnyOrigin],
    XS: TileTensor[DType.float32, xs_layout, MutAnyOrigin],
    SC: TileTensor[DType.float32, sc_layout, MutAnyOrigin],
    C: TileTensor[mut=True, out_dtype, c_layout, MutAnyOrigin],
):
    """Decode packed W4A8 tiles in shared memory and consume them in MMA."""
    comptime MMA_M = 16
    comptime MMA_N = 8
    comptime MMA_K = 32
    comptime assert WM % MMA_M == 0 and WN % MMA_N == 0 and BK == MMA_K

    var warp_y, warp_x = udivmod(warp_id(), BN // WN)
    var C_block = C.tile[BM, BN](block_idx.y, block_idx.x)
    var A_smem = stack_allocation[DType.int8, address_space=.SHARED](
        row_major[BM, BK]()
    )
    var packed_smem = stack_allocation[DType.int8, address_space=.SHARED](
        row_major[BN, BK // 2]()
    )
    var scales_smem = stack_allocation[
        DType.float8_e4m3fn, address_space=.SHARED
    ](row_major[BN, BK // 16]())
    var codebook_smem = stack_allocation[DType.float32, address_space=.SHARED](
        row_major[16]()
    )
    var c_reg = stack_allocation[DType.int32, address_space=.LOCAL](
        row_major[WM // MMA_M, (WN * 4) // MMA_N]()
    ).fill(0)
    var c_smem = stack_allocation[DType.int32, address_space=.SHARED](
        row_major[BM, BN]()
    )
    var warp_region = c_smem.tile[WM, WN](warp_y, warp_x)
    if thread_idx.x < 16:
        codebook_smem[thread_idx.x] = CB[thread_idx.x]

    for k_i in range(Int(A.dim[1]()) // BK):
        var A_tile = A.tile[BM, BK](block_idx.y, k_i)
        var packed_tile = W.tile[BN, BK // 2](block_idx.x, k_i)
        var scales_tile = S.tile[BN, BK // 16](block_idx.x, k_i)
        copy_dram_to_sram[thread_layout=row_major[2, 32]()](A_smem, A_tile)
        copy_dram_to_sram[thread_layout=row_major[4, 16]()](packed_smem, packed_tile)
        copy_dram_to_sram[thread_layout=row_major[32, 2]()](scales_smem, scales_tile)
        barrier()

        var A_warp = A_smem.tile[WM, BK](warp_y, 0)
        comptime for mma_m in range(WM // MMA_M):
            comptime for mma_n in range(WN // MMA_N):
                var out_reg = c_reg.tile[1, 4](mma_m, mma_n)
                var a_reg = stack_allocation[DType.int8, address_space=.LOCAL](
                    row_major[1, 16]()
                )
                var b_reg = stack_allocation[DType.int8, address_space=.LOCAL](
                    row_major[8, 1]()
                )
                comptime for register in range(16):
                    var row = lane_id() // 4 + 8 * ((register // 4) % 2)
                    var k = (lane_id() % 4) * 4 + 16 * ((register // 4) // 2) + register % 4
                    a_reg[0, register] = A_warp.tile[MMA_M, MMA_K](mma_m, 0)[row, k]
                comptime for register in range(8):
                    var col = lane_id() // 4
                    var k = (lane_id() % 4) * 4 + 16 * (register // 4) + register % 4
                    var n = warp_x * WN + mma_n * MMA_N + col
                    var packed = packed_smem[n, k // 2].cast[DType.uint8]()
                    var code = (packed.cast[DType.int32]() >> Int32(4 * (k & 1))) & 0xF
                    var decoded = round(
                        codebook_smem[code]
                        * scales_smem[n, k // 16].cast[DType.float32]()
                    )
                    decoded = min(max(decoded, Float32(-127)), Float32(127))
                    b_reg[register, 0] = decoded.cast[DType.int8]()
                var a_packed = a_reg.vectorize[1, 16]().load((0, 0))
                var b_packed = b_reg.vectorize[8, 1]().load((0, 0))
                var c_packed = out_reg.vectorize[1, 4]().load((0, 0))
                var a_words = bitcast[DType.uint32, 4](a_packed)
                var b_words = bitcast[DType.uint32, 2](b_packed)
                var result = inlined_assembly[
                    (
                        "mma.sync.aligned.m16n8k32.row.col.s32.s8.s8.s32 "
                        "{$0, $1, $2, $3}, {$4, $5, $6, $7}, {$8, $9}, "
                        "{$10, $11, $12, $13};"
                    ),
                    _RegisterPackType[Int32, Int32, Int32, Int32],
                    constraints="=r,=r,=r,=r,r,r,r,r,r,r,r,r,r,r",
                ](
                    a_words[0], a_words[1], a_words[2], a_words[3],
                    b_words[0], b_words[1],
                    c_packed[0], c_packed[1], c_packed[2], c_packed[3],
                )
                out_reg.vectorize[1, 4]().store(
                    (0, 0), SIMD[DType.int32, 4](result[0], result[1], result[2], result[3])
                )
        barrier()

    comptime for mma_m in range(WM // MMA_M):
        comptime for mma_n in range(WN // MMA_N):
            var C_tile = warp_region.tile[MMA_M, MMA_N](mma_m, mma_n)
            var fragment = c_reg.tile[1, 4](mma_m, mma_n)
            comptime for register in range(4):
                var row = lane_id() // 4 + (register // 2) * 8
                var col = (lane_id() % 4) * 2 + register % 2
                C_tile[row, col] = fragment[0, register]
    barrier()
    for flat in range(Int(thread_idx.x), BM * BN, NUM_THREADS):
        var i, j = udivmod(flat, BN)
        comptime if scaled:
            var row = block_idx.y * BM + i
            var col = block_idx.x * BN + j
            var value = c_smem[i, j].cast[DType.float32]() * XS[row] * SC[col]
            C_block[i, j] = value.cast[out_dtype]()
        else:
            C_block[i, j] = c_smem[i, j].cast[out_dtype]()


@extensibility.register("h3_w4a8_mma")
struct H3W4A8MMA:
    """Packed W4A8 decode fused into signed INT8 tensor-core GEMM."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.int32, rank=2, ...],
        x: InputTensor[dtype=DType.int8, rank=2, ...],
        weight: InputTensor[dtype=DType.int8, rank=2, ...],
        scales: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        codebook: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var weight_tt = weight.to_tile_tensor().as_unsafe_any_origin()
        var scales_tt = scales.to_tile_tensor().as_unsafe_any_origin()
        var codebook_tt = codebook.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var N = Int(weight_tt.dim[0]())
        var K = Int(x_tt.dim[1]())
        if M % 64 != 0 or N % 64 != 0 or K % 256 != 0:
            raise Error("h3_w4a8_mma requires M,N divisible by 64 and K by 256")
        if Int(weight_tt.dim[1]()) != K // 2 or Int(scales_tt.dim[1]()) != K // 16:
            raise Error("h3_w4a8_mma got inconsistent packed shapes")
        comptime BM = 64
        comptime BN = 64
        comptime BK = 32
        comptime WM = 64
        comptime WN = 32
        comptime NUM_WARPS = (BM // WM) * (BN // WN)
        comptime NUM_THREADS = NUM_WARPS * WARP_SIZE
        comptime kernel = w4a8_mma_kernel[
            type_of(x_tt).LayoutType,
            type_of(weight_tt).LayoutType,
            type_of(scales_tt).LayoutType,
            type_of(codebook_tt).LayoutType,
            type_of(codebook_tt).LayoutType,
            type_of(codebook_tt).LayoutType,
            type_of(out_tt).LayoutType,
            DType.int32,
            False,
            BM, BN, BK, WM, WN, NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt, weight_tt, scales_tt, codebook_tt, codebook_tt, codebook_tt, out_tt,
            grid_dim=(ceildiv(N, BN), ceildiv(M, BM)), block_dim=NUM_THREADS,
        )


@extensibility.register("h3_w4a8_linear")
struct H3W4A8Linear:
    """Packed W4A8 MMA with FP32 row/channel scaling and BF16 output."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.bfloat16, rank=2, ...],
        x: InputTensor[dtype=DType.int8, rank=2, ...],
        x_scales: InputTensor[dtype=DType.float32, rank=1, ...],
        weight: InputTensor[dtype=DType.int8, rank=2, ...],
        scales: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        codebook: InputTensor[dtype=DType.float32, rank=1, ...],
        channel_scales: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var xs_tt = x_scales.to_tile_tensor().as_unsafe_any_origin()
        var weight_tt = weight.to_tile_tensor().as_unsafe_any_origin()
        var scales_tt = scales.to_tile_tensor().as_unsafe_any_origin()
        var codebook_tt = codebook.to_tile_tensor().as_unsafe_any_origin()
        var channel_tt = channel_scales.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var N = Int(weight_tt.dim[0]())
        var K = Int(x_tt.dim[1]())
        if M % 64 != 0 or N % 64 != 0 or K % 256 != 0:
            raise Error("h3_w4a8_linear requires M,N divisible by 64 and K by 256")
        if Int(weight_tt.dim[1]()) != K // 2 or Int(scales_tt.dim[1]()) != K // 16:
            raise Error("h3_w4a8_linear got inconsistent packed shapes")
        comptime BM = 64
        comptime BN = 64
        comptime BK = 32
        comptime WM = 64
        comptime WN = 32
        comptime NUM_WARPS = (BM // WM) * (BN // WN)
        comptime NUM_THREADS = NUM_WARPS * WARP_SIZE
        comptime kernel = w4a8_mma_kernel[
            type_of(x_tt).LayoutType,
            type_of(weight_tt).LayoutType,
            type_of(scales_tt).LayoutType,
            type_of(codebook_tt).LayoutType,
            type_of(xs_tt).LayoutType,
            type_of(channel_tt).LayoutType,
            type_of(out_tt).LayoutType,
            DType.bfloat16,
            True,
            BM, BN, BK, WM, WN, NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt, weight_tt, scales_tt, codebook_tt, xs_tt, channel_tt, out_tt,
            grid_dim=(ceildiv(N, BN), ceildiv(M, BM)), block_dim=NUM_THREADS,
        )


@extensibility.register("h3_int8_mma")
struct H3Int8MMA:
    """Unscaled signed INT8 x INT8 -> INT32 tensor-core GEMM; GPU only."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.int32, rank=2, ...],
        x: InputTensor[dtype=DType.int8, rank=2, ...],
        weight: InputTensor[dtype=DType.int8, rank=2, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var weight_tt = weight.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var N = Int(weight_tt.dim[0]())
        var K = Int(x_tt.dim[1]())
        if M % 64 != 0 or N % 64 != 0 or K % 32 != 0:
            raise Error("h3_int8_mma requires M,N divisible by 64 and K by 32")
        comptime BM = 64
        comptime BN = 64
        comptime BK = 32
        comptime WM = 64
        comptime WN = 32
        comptime NUM_WARPS = (BM // WM) * (BN // WN)
        comptime NUM_THREADS = NUM_WARPS * WARP_SIZE
        comptime kernel = int8_mma_kernel[
            type_of(x_tt).LayoutType,
            type_of(weight_tt).LayoutType,
            type_of(out_tt).LayoutType,
            BM, BN, BK, WM, WN, NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt, weight_tt, out_tt,
            grid_dim=(ceildiv(N, BN), ceildiv(M, BM)), block_dim=NUM_THREADS,
        )
