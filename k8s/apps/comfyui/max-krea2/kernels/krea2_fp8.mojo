# Custom Krea 2 operations for MAX 26.7. The Python graph remains the model
# frontend; these kernels are compiled by MAX and run on the selected device.

import extensibility

from max.gpu.host import DeviceContext
from extensibility import InputTensor, OutputTensor, foreach
from std.utils.coord import Coord
from std.utils import StaticTuple


@extensibility.register("krea2_nvfp4_dequant")
struct Krea2NVFP4Dequant:
    """Decode low-first packed NVFP4 with plain [N, K/16] scales to BF16."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor[dtype=DType.bfloat16, rank=2, ...],
        weight: InputTensor[dtype=DType.uint8, rank=2, ...],
        scales: InputTensor[dtype=DType.float8_e4m3fn, rank=2, ...],
        global_scale: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime E2M1 = StaticTuple[Float32, 16](
            0.0, 0.5, 1.0, 1.5, 2.0, 3.0, 4.0, 6.0,
            -0.0, -0.5, -1.0, -1.5, -2.0, -3.0, -4.0, -6.0,
        )

        @inline(.always)
        def dequant[width: Int](idx: Coord) {var} -> SIMD[output.dtype, width]:
            var result = SIMD[output.dtype, width]()
            var scalar_scale = global_scale.load[1](Coord(0))[0].cast[output.dtype]()
            for lane in range(width):
                var row = Int(idx[0])
                var col = Int(idx[1]) + lane
                var packed = weight.load[1](Coord(row, col // 2))[0].cast[DType.int32]()
                var code = (packed >> Int32(4 * (col & 1))) & 0xF
                var block_scale = scales.load[1](Coord(row, col // 16))[0].cast[output.dtype]()
                result[lane] = E2M1[code].cast[output.dtype]() * (block_scale * scalar_scale)
            return result

        foreach[target=target](dequant, output, ctx)


@extensibility.register("krea2_fp8_dequant")
struct Krea2FP8Dequant:
    """Match MAX's FP8->BF16 cast followed by a BF16 scalar multiply."""

    @staticmethod
    def execute[
        target: StaticString,
    ](
        output: OutputTensor,
        weight: InputTensor[
            dtype=DType.float8_e4m3fn,
            rank=output.rank,
            ...,
        ],
        scale: InputTensor[dtype=DType.float32, rank=1, ...],
        ctx: DeviceContext,
    ) raises:
        comptime assert output.dtype == DType.bfloat16

        @inline(.always)
        def dequant[width: Int](idx: Coord) {var} -> SIMD[output.dtype, width]:
            # This load must stay inside the device function. Reading a GPU
            # tensor from execute()'s host side dereferences a device pointer.
            var scalar_scale = scale.load[1](Coord(0))[0].cast[output.dtype]()
            return weight.load[width](idx).cast[output.dtype]() * scalar_scale

        foreach[target=target](dequant, output, ctx)
