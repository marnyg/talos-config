# Custom Krea 2 operations for MAX 26.7. The Python graph remains the model
# frontend; these kernels are compiled by MAX and run on the selected device.

import extensibility

from max.gpu.host import DeviceContext
from extensibility import InputTensor, OutputTensor, foreach
from std.utils.coord import Coord


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
