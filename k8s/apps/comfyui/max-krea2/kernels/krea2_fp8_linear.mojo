# Experimental fused Krea 2 FP8-weight linear for MAX 26.7.
# Adapted from Modular's max/examples/custom_ops matrix_multiplication.mojo
# (max/v26.7.0), licensed Apache-2.0 WITH LLVM-exception.
#
# x: [M, K] BF16, weight: [N, K] FP8 E4M3, scale: [1] FP32.
# output: [M, N] BF16 = x @ (weight * scale).T.
#
# Includes scalar and tensor-core tiled variants, plus an experimental direct
# FP8 MMA path. None materializes a full BF16 weight. Shape alignment rules
# are checked at each operation boundary; the Krea benchmark shapes satisfy
# the tensor-core path used by default in the measurements.

from std.math import ceildiv
from std.math.uutils import udivmod

import extensibility

from max.gpu import (
    MAX_THREADS_PER_BLOCK_METADATA,
    WARP_SIZE,
    block_idx,
    thread_idx,
    warp_id,
)
from max.gpu.host import DeviceContext
from max.gpu.sync import barrier
from layout import TensorLayout, TileTensor, row_major, stack_allocation
from layout.tensor_core import TensorCore
from layout.tile_io import copy_dram_to_sram
from extensibility import InputTensor, OutputTensor
from std.utils import StaticTuple
from std.utils.coord import Coord
from std.utils.index import Index


@__llvm_metadata(MAX_THREADS_PER_BLOCK_METADATA=StaticTuple[Int32, 1](1024))
def tiled_fp8_linear[
    a_layout: TensorLayout,
    w_layout: TensorLayout,
    s_layout: TensorLayout,
    c_layout: TensorLayout,
    BM: Int,
    BN: Int,
    BK: Int,
    NUM_THREADS: Int,
](
    a: TileTensor[DType.bfloat16, a_layout, MutAnyOrigin],
    w: TileTensor[DType.float8_e4m3fn, w_layout, MutAnyOrigin],
    s: TileTensor[DType.float32, s_layout, MutAnyOrigin],
    c: TileTensor[mut=True, DType.bfloat16, c_layout, MutAnyOrigin],
):
    comptime assert a.flat_rank == 2
    comptime assert w.flat_rank == 2
    comptime assert s.flat_rank == 1
    comptime assert c.flat_rank == 2

    var row, col = udivmod(thread_idx.x, BN)
    var dst = c.tile[BM, BN](block_idx.y, block_idx.x)
    var a_smem = stack_allocation[dtype=DType.bfloat16, address_space=.SHARED](
        row_major[BM, BK]()
    )
    # Keep weights packed in shared memory too. [N, K] needs no transposed
    # tile: each thread reads w_smem[col, k] in the compute loop.
    var w_smem = stack_allocation[
        dtype=DType.float8_e4m3fn, address_space=.SHARED
    ](row_major[BN, BK]())
    var acc = Float32(0)
    var scalar_scale = s.load[width=1](Coord(0))[0]

    comptime load_a_layout = row_major[NUM_THREADS // BK, BK]()
    comptime load_w_layout = row_major[NUM_THREADS // BK, BK]()

    for block in range(Int(a.dim[1]()) // BK):
        var a_tile = a.tile[BM, BK](block_idx.y, block)
        var w_tile = w.tile[BN, BK](block_idx.x, block)
        # MAX 26.7's async copier accepts only 4/8/16-byte elements,
        # excluding both BF16 activations and FP8 weights.
        copy_dram_to_sram[thread_layout=load_a_layout](a_smem, a_tile)
        copy_dram_to_sram[thread_layout=load_w_layout](w_smem, w_tile)
        barrier()

        comptime for k in range(BK):
            # Round the scaled weight to BF16 before the product, matching
            # the original MAX dequant+cuBLAS path. Accumulate in FP32.
            var w_bf16 = (
                w_smem[col, k].cast[DType.bfloat16]()
                * scalar_scale.cast[DType.bfloat16]()
            )
            acc += (
                a_smem[row, k].cast[DType.float32]()
                * w_bf16.cast[DType.float32]()
            )
        barrier()

    dst[row, col] = acc.cast[DType.bfloat16]()


def tensor_core_fp8_linear[
    a_layout: TensorLayout,
    w_layout: TensorLayout,
    s_layout: TensorLayout,
    c_layout: TensorLayout,
    BM: Int,
    BN: Int,
    BK: Int,
    WM: Int,
    WN: Int,
    NUM_THREADS: Int,
](
    A: TileTensor[DType.bfloat16, a_layout, MutAnyOrigin],
    W: TileTensor[DType.float8_e4m3fn, w_layout, MutAnyOrigin],
    S: TileTensor[DType.float32, s_layout, MutAnyOrigin],
    C: TileTensor[mut=True, DType.bfloat16, c_layout, MutAnyOrigin],
):
    """FP8 weight tile -> BF16 shared tile -> BF16 tensor-core MMA.

    The accumulator stays FP32 and is staged through a small FP32 shared
    tile before the BF16 global write. Emitting BF16 directly halves every
    projection's transient output, which matters at 1024^2 where the FP8
    weights already fill the card's budget.
    """
    comptime MMA_M = 16
    comptime MMA_N = 8
    comptime MMA_K = 16
    comptime assert WM % MMA_M == 0 and WN % MMA_N == 0 and BK % MMA_K == 0

    var warp_y, warp_x = udivmod(warp_id(), BN // WN)
    var C_block = C.tile[BM, BN](block_idx.y, block_idx.x)
    var C_warp = C_block.tile[WM, WN](warp_y, warp_x)
    var mma = TensorCore[
        DType.float32,
        DType.bfloat16,
        Index(MMA_M, MMA_N, MMA_K),
        transpose_b=True,
    ]()
    var A_smem = stack_allocation[dtype=DType.bfloat16, address_space=.SHARED](
        row_major[BM, BK]()
    )
    var W_smem = stack_allocation[
        dtype=DType.float8_e4m3fn, address_space=.SHARED
    ](row_major[BN, BK]())
    var B_smem = stack_allocation[dtype=DType.bfloat16, address_space=.SHARED](
        row_major[BN, BK]()
    )
    var c_reg = stack_allocation[DType.float32, address_space=.LOCAL](
        row_major[WM // MMA_M, (WN * 4) // MMA_N]()
    ).fill(0)
    # store_d requires the destination dtype to equal the accumulator dtype,
    # so the FP32 result lands here first and is cast on the way to global.
    var c_smem = stack_allocation[DType.float32, address_space=.SHARED](
        row_major[BM, BN]()
    )
    var warp_region = c_smem.tile[WM, WN](warp_y, warp_x)
    var scalar_scale = S.load[width=1](Coord(0))[0].cast[DType.bfloat16]()

    for k_i in range(Int(A.dim[1]()) // BK):
        var A_tile = A.tile[BM, BK](block_idx.y, k_i)
        var W_tile = W.tile[BN, BK](block_idx.x, k_i)
        copy_dram_to_sram[thread_layout=row_major[2, 32]()](A_smem, A_tile)
        copy_dram_to_sram[thread_layout=row_major[2, 32]()](W_smem, W_tile)
        barrier()

        # Dequantize just this tile in its native [N, K] layout. TensorCore
        # transposes B during fragment loads, avoiding strided shared writes.
        for flat in range(Int(thread_idx.x), BN * BK, NUM_THREADS):
            var n, k = udivmod(flat, BK)
            B_smem[n, k] = W_smem[n, k].cast[DType.bfloat16]() * scalar_scale
        barrier()

        var A_warp = A_smem.tile[WM, BK](warp_y, 0)
        var B_warp = B_smem.tile[WN, BK](warp_x, 0)
        comptime for mma_k in range(BK // MMA_K):
            comptime for mma_m in range(WM // MMA_M):
                comptime for mma_n in range(WN // MMA_N):
                    var out_reg = c_reg.tile[1, 4](mma_m, mma_n)
                    var a_reg = mma.load_a(
                        A_warp.tile[MMA_M, MMA_K](mma_m, mma_k)
                    )
                    var b_reg = mma.load_b(
                        B_warp.tile[MMA_N, MMA_K](mma_n, mma_k)
                    )
                    out_reg.copy_from(mma.mma_op(a_reg, b_reg, out_reg))
        barrier()

    comptime for mma_m in range(WM // MMA_M):
        comptime for mma_n in range(WN // MMA_N):
            var S_tile = warp_region.tile[MMA_M, MMA_N](mma_m, mma_n)
            mma.store_d(S_tile, c_reg.tile[1, 4](mma_m, mma_n))
    barrier()
    for flat in range(Int(thread_idx.x), BM * BN, NUM_THREADS):
        var i, j = udivmod(flat, BN)
        C_block[i, j] = c_smem[i, j].cast[DType.bfloat16]()


def tensor_core_nvfp4_linear[
    a_layout: TensorLayout,
    w_layout: TensorLayout,
    s_layout: TensorLayout,
    g_layout: TensorLayout,
    c_layout: TensorLayout,
    BM: Int,
    BN: Int,
    BK: Int,
    WM: Int,
    WN: Int,
    NUM_THREADS: Int,
](
    A: TileTensor[DType.bfloat16, a_layout, MutAnyOrigin],
    W: TileTensor[DType.uint8, w_layout, MutAnyOrigin],
    S: TileTensor[DType.float8_e4m3fn, s_layout, MutAnyOrigin],
    G: TileTensor[DType.float32, g_layout, MutAnyOrigin],
    C: TileTensor[mut=True, DType.bfloat16, c_layout, MutAnyOrigin],
):
    """Packed NVFP4 weight tile -> BF16 shared tile -> BF16 MMA.

    W is low-nibble-first [N, K/2]. S is deinterleaved [N, K/16].
    Comfy's W4A16 dequantization rounds global_scale * block_scale to BF16
    before multiplying the exactly representable E2M1 value; do the same.
    """
    comptime MMA_M = 16
    comptime MMA_N = 8
    comptime MMA_K = 16
    comptime assert WM % MMA_M == 0 and WN % MMA_N == 0 and BK % MMA_K == 0
    comptime E2M1 = StaticTuple[Float32, 16](
        0.0, 0.5, 1.0, 1.5, 2.0, 3.0, 4.0, 6.0,
        -0.0, -0.5, -1.0, -1.5, -2.0, -3.0, -4.0, -6.0,
    )

    var warp_y, warp_x = udivmod(warp_id(), BN // WN)
    var C_block = C.tile[BM, BN](block_idx.y, block_idx.x)
    var mma = TensorCore[
        DType.float32,
        DType.bfloat16,
        Index(MMA_M, MMA_N, MMA_K),
        transpose_b=True,
    ]()
    var A_smem = stack_allocation[dtype=DType.bfloat16, address_space=.SHARED](
        row_major[BM, BK]()
    )
    var W_smem = stack_allocation[dtype=DType.uint8, address_space=.SHARED](
        row_major[BN, BK // 2]()
    )
    var S_smem = stack_allocation[
        dtype=DType.float8_e4m3fn, address_space=.SHARED
    ](row_major[BN, BK // 16]())
    var B_smem = stack_allocation[dtype=DType.bfloat16, address_space=.SHARED](
        row_major[BN, BK]()
    )
    var c_reg = stack_allocation[DType.float32, address_space=.LOCAL](
        row_major[WM // MMA_M, (WN * 4) // MMA_N]()
    ).fill(0)
    var c_smem = stack_allocation[DType.float32, address_space=.SHARED](
        row_major[BM, BN]()
    )
    var warp_region = c_smem.tile[WM, WN](warp_y, warp_x)
    var global_scale = G.load[width=1](Coord(0))[0].cast[DType.bfloat16]()

    for k_i in range(Int(A.dim[1]()) // BK):
        var A_tile = A.tile[BM, BK](block_idx.y, k_i)
        var W_tile = W.tile[BN, BK // 2](block_idx.x, k_i)
        var S_tile = S.tile[BN, BK // 16](block_idx.x, k_i)
        copy_dram_to_sram[thread_layout=row_major[2, 32]()](A_smem, A_tile)
        copy_dram_to_sram[thread_layout=row_major[4, 16]()](W_smem, W_tile)
        copy_dram_to_sram[thread_layout=row_major[32, 2]()](S_smem, S_tile)
        barrier()

        for flat in range(Int(thread_idx.x), BN * BK, NUM_THREADS):
            var n, k = udivmod(flat, BK)
            var packed = W_smem[n, k // 2].cast[DType.int32]()
            var code = (packed >> Int32(4 * (k & 1))) & 0xF
            var block_scale = (
                S_smem[n, k // 16].cast[DType.bfloat16]()
                * global_scale
            )
            B_smem[n, k] = E2M1[code].cast[DType.bfloat16]() * block_scale
        barrier()

        var A_warp = A_smem.tile[WM, BK](warp_y, 0)
        var B_warp = B_smem.tile[WN, BK](warp_x, 0)
        comptime for mma_k in range(BK // MMA_K):
            comptime for mma_m in range(WM // MMA_M):
                comptime for mma_n in range(WN // MMA_N):
                    var out_reg = c_reg.tile[1, 4](mma_m, mma_n)
                    var a_reg = mma.load_a(
                        A_warp.tile[MMA_M, MMA_K](mma_m, mma_k)
                    )
                    var b_reg = mma.load_b(
                        B_warp.tile[MMA_N, MMA_K](mma_n, mma_k)
                    )
                    out_reg.copy_from(mma.mma_op(a_reg, b_reg, out_reg))
        barrier()

    comptime for mma_m in range(WM // MMA_M):
        comptime for mma_n in range(WN // MMA_N):
            var S_tile = warp_region.tile[MMA_M, MMA_N](mma_m, mma_n)
            mma.store_d(S_tile, c_reg.tile[1, 4](mma_m, mma_n))
    barrier()
    for flat in range(Int(thread_idx.x), BM * BN, NUM_THREADS):
        var i, j = udivmod(flat, BN)
        C_block[i, j] = c_smem[i, j].cast[DType.bfloat16]()


@extensibility.register("krea2_nvfp4_linear_tc")
struct Krea2NVFP4LinearTensorCore:
    """Fused weight-only NVFP4 dequantization and BF16 linear; GPU only."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.bfloat16, rank=2, ...],
        x: InputTensor[dtype=DType.bfloat16, rank=2, ...],
        weight: InputTensor[dtype=DType.uint8, rank=2, ...],
        scales: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        global_scale: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var weight_tt = weight.to_tile_tensor().as_unsafe_any_origin()
        var scales_tt = scales.to_tile_tensor().as_unsafe_any_origin()
        var global_scale_tt = global_scale.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var N = Int(weight_tt.dim[0]())
        var K = Int(x_tt.dim[1]())
        if M % 64 != 0 or N % 64 != 0 or K % 32 != 0:
            raise Error(
                "krea2_nvfp4_linear_tc requires M,N divisible by 64 and K by 32"
            )
        if Int(weight_tt.dim[1]()) != K // 2 or Int(scales_tt.dim[1]()) != K // 16:
            raise Error("krea2_nvfp4_linear_tc got inconsistent packed shapes")

        comptime BM = 64
        comptime BN = 64
        comptime BK = 32
        comptime WM = 64
        comptime WN = 32
        comptime NUM_WARPS = (BM // WM) * (BN // WN)
        comptime NUM_THREADS = NUM_WARPS * WARP_SIZE
        comptime kernel = tensor_core_nvfp4_linear[
            type_of(x_tt).LayoutType,
            type_of(weight_tt).LayoutType,
            type_of(scales_tt).LayoutType,
            type_of(global_scale_tt).LayoutType,
            type_of(out_tt).LayoutType,
            BM,
            BN,
            BK,
            WM,
            WN,
            NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt,
            weight_tt,
            scales_tt,
            global_scale_tt,
            out_tt,
            grid_dim=(ceildiv(N, BN), ceildiv(M, BM)),
            block_dim=NUM_THREADS,
        )


@extensibility.register("krea2_fp8_linear_tc")
struct Krea2FP8LinearTensorCore:
    """Fused FP8 dequantization and tensor-core BF16 linear; GPU only."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.bfloat16, rank=2, ...],
        x: InputTensor[dtype=DType.bfloat16, rank=2, ...],
        weight: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        scale: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var weight_tt = weight.to_tile_tensor().as_unsafe_any_origin()
        var scale_tt = scale.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var N = Int(weight_tt.dim[0]())
        var K = Int(x_tt.dim[1]())
        if M % 64 != 0 or N % 64 != 0 or K % 32 != 0:
            raise Error(
                "krea2_fp8_linear_tc requires M,N divisible by 64 and K by 32"
            )

        comptime BM = 64
        comptime BN = 64
        comptime BK = 32
        comptime WM = 64
        comptime WN = 32
        comptime NUM_WARPS = (BM // WM) * (BN // WN)
        comptime NUM_THREADS = NUM_WARPS * WARP_SIZE
        comptime kernel = tensor_core_fp8_linear[
            type_of(x_tt).LayoutType,
            type_of(weight_tt).LayoutType,
            type_of(scale_tt).LayoutType,
            type_of(out_tt).LayoutType,
            BM,
            BN,
            BK,
            WM,
            WN,
            NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt,
            weight_tt,
            scale_tt,
            out_tt,
            grid_dim=(ceildiv(N, BN), ceildiv(M, BM)),
            block_dim=NUM_THREADS,
        )


def direct_fp8_mma[
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
    A: TileTensor[DType.float8_e4m3fn, a_layout, MutAnyOrigin],
    W: TileTensor[DType.float8_e4m3fn, w_layout, MutAnyOrigin],
    C: TileTensor[mut=True, DType.float32, c_layout, MutAnyOrigin],
):
    """Unscaled FP8xFP8 tensor-core MMA; scales are applied in the graph."""
    comptime MMA_M = 16
    comptime MMA_N = 8
    comptime MMA_K = 32
    comptime assert WM % MMA_M == 0 and WN % MMA_N == 0 and BK == MMA_K

    var warp_y, warp_x = udivmod(warp_id(), BN // WN)
    var C_warp = C.tile[BM, BN](block_idx.y, block_idx.x).tile[WM, WN](
        warp_y, warp_x
    )
    var mma = TensorCore[
        DType.float32, DType.float8_e4m3fn, Index(MMA_M, MMA_N, MMA_K)
    ]()
    var A_smem = stack_allocation[
        dtype=DType.float8_e4m3fn, address_space=.SHARED
    ](row_major[BM, BK]())
    var W_smem = stack_allocation[
        dtype=DType.float8_e4m3fn, address_space=.SHARED
    ](row_major[BN, BK]())
    var B_smem = stack_allocation[
        dtype=DType.float8_e4m3fn, address_space=.SHARED
    ](row_major[BK, BN]())
    var c_reg = stack_allocation[DType.float32, address_space=.LOCAL](
        row_major[WM // MMA_M, (WN * 4) // MMA_N]()
    ).fill(0)

    for k_i in range(Int(A.dim[1]()) // BK):
        var A_tile = A.tile[BM, BK](block_idx.y, k_i)
        var W_tile = W.tile[BN, BK](block_idx.x, k_i)
        copy_dram_to_sram[thread_layout=row_major[4, 8]()](A_smem, A_tile)
        copy_dram_to_sram[thread_layout=row_major[4, 8]()](W_smem, W_tile)
        barrier()

        for flat in range(Int(thread_idx.x), BN * BK, NUM_THREADS):
            var n, k = udivmod(flat, BK)
            B_smem[k, n] = W_smem[n, k]
        barrier()

        var A_warp = A_smem.tile[WM, BK](warp_y, 0)
        var B_warp = B_smem.tile[BK, WN](0, warp_x)
        comptime for mma_m in range(WM // MMA_M):
            comptime for mma_n in range(WN // MMA_N):
                var out_reg = c_reg.tile[1, 4](mma_m, mma_n)
                var a_reg = mma.load_a(A_warp.tile[MMA_M, MMA_K](mma_m, 0))
                var b_reg = mma.load_b(B_warp.tile[MMA_K, MMA_N](0, mma_n))
                out_reg.copy_from(mma.mma_op(a_reg, b_reg, out_reg))
        barrier()

    comptime for mma_m in range(WM // MMA_M):
        comptime for mma_n in range(WN // MMA_N):
            var C_tile = C_warp.tile[MMA_M, MMA_N](mma_m, mma_n)
            mma.store_d(C_tile, c_reg.tile[1, 4](mma_m, mma_n))


@extensibility.register("krea2_fp8_mma")
struct Krea2FP8MMA:
    """Unscaled direct FP8 tensor-core GEMM; GPU only, SM90+."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.float32, rank=2, ...],
        x: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        weight: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
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
            raise Error(
                "krea2_fp8_mma requires M,N divisible by 64 and K by 32"
            )

        comptime BM = 64
        comptime BN = 64
        comptime BK = 32
        comptime WM = 32
        comptime WN = 32
        comptime NUM_WARPS = (BM // WM) * (BN // WN)
        comptime NUM_THREADS = NUM_WARPS * WARP_SIZE
        comptime kernel = direct_fp8_mma[
            type_of(x_tt).LayoutType,
            type_of(weight_tt).LayoutType,
            type_of(out_tt).LayoutType,
            BM,
            BN,
            BK,
            WM,
            WN,
            NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt,
            weight_tt,
            out_tt,
            grid_dim=(ceildiv(N, BN), ceildiv(M, BM)),
            block_dim=NUM_THREADS,
        )


@extensibility.register("krea2_fp8_linear")
struct Krea2FP8Linear:
    """Fused FP8 dequantization and tiled BF16 linear; GPU only."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[rank=2, ...],
        x: InputTensor[dtype=DType.bfloat16, rank=2, ...],
        weight: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        scale: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert target == "gpu"
        comptime assert output.dtype == DType.bfloat16

        var x_tt = x.to_tile_tensor().as_unsafe_any_origin()
        var weight_tt = weight.to_tile_tensor().as_unsafe_any_origin()
        var scale_tt = scale.to_tile_tensor().as_unsafe_any_origin()
        var out_tt = output.to_tile_tensor().as_unsafe_any_origin()
        var M = Int(x_tt.dim[0]())
        var N = Int(weight_tt.dim[0]())
        var K = Int(x_tt.dim[1]())

        if M % 32 != 0 or N % 32 != 0 or K % 32 != 0:
            raise Error("krea2_fp8_linear requires M, N and K divisible by 32")

        comptime BM = 32
        comptime BN = 32
        comptime BK = 32
        comptime NUM_THREADS = BM * BN
        comptime kernel = tiled_fp8_linear[
            type_of(x_tt).LayoutType,
            type_of(weight_tt).LayoutType,
            type_of(scale_tt).LayoutType,
            type_of(out_tt).LayoutType,
            BM,
            BN,
            BK,
            NUM_THREADS,
        ]
        var gpu_ctx = ctx
        gpu_ctx.enqueue_function[kernel](
            x_tt,
            weight_tt,
            scale_tt,
            out_tt,
            grid_dim=(ceildiv(N, BN), ceildiv(M, BM)),
            block_dim=NUM_THREADS,
        )
