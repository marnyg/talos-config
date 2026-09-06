package iroh

// #include <iroh.h>
import "C"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"runtime"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"unsafe"
)

// This is needed, because as of go 1.24
// type RustBuffer C.RustBuffer cannot have methods,
// RustBuffer is treated as non-local type
type GoRustBuffer struct {
	inner C.RustBuffer
}

type RustBufferI interface {
	AsReader() *bytes.Reader
	Free()
	ToGoBytes() []byte
	Data() unsafe.Pointer
	Len() uint64
	Capacity() uint64
}

// C.RustBuffer fields exposed as an interface so they can be accessed in different Go packages.
// See https://github.com/golang/go/issues/13467
type ExternalCRustBuffer interface {
	Data() unsafe.Pointer
	Len() uint64
	Capacity() uint64
}

func RustBufferFromC(b C.RustBuffer) ExternalCRustBuffer {
	return GoRustBuffer{
		inner: b,
	}
}

func CFromRustBuffer(b ExternalCRustBuffer) C.RustBuffer {
	return C.RustBuffer{
		capacity: C.uint64_t(b.Capacity()),
		len:      C.uint64_t(b.Len()),
		data:     (*C.uchar)(b.Data()),
	}
}

func RustBufferFromExternal(b ExternalCRustBuffer) GoRustBuffer {
	return GoRustBuffer{
		inner: C.RustBuffer{
			capacity: C.uint64_t(b.Capacity()),
			len:      C.uint64_t(b.Len()),
			data:     (*C.uchar)(b.Data()),
		},
	}
}

func (cb GoRustBuffer) Capacity() uint64 {
	return uint64(cb.inner.capacity)
}

func (cb GoRustBuffer) Len() uint64 {
	return uint64(cb.inner.len)
}

func (cb GoRustBuffer) Data() unsafe.Pointer {
	return unsafe.Pointer(cb.inner.data)
}

func (cb GoRustBuffer) AsReader() *bytes.Reader {
	b := unsafe.Slice((*byte)(cb.inner.data), C.uint64_t(cb.inner.len))
	return bytes.NewReader(b)
}

func (cb GoRustBuffer) Free() {
	rustCall(func(status *C.RustCallStatus) bool {
		C.ffi_iroh_ffi_rustbuffer_free(cb.inner, status)
		return false
	})
}

func (cb GoRustBuffer) ToGoBytes() []byte {
	return C.GoBytes(unsafe.Pointer(cb.inner.data), C.int(cb.inner.len))
}

func stringToRustBuffer(str string) C.RustBuffer {
	return bytesToRustBuffer([]byte(str))
}

func bytesToRustBuffer(b []byte) C.RustBuffer {
	if len(b) == 0 {
		return C.RustBuffer{}
	}
	// We can pass the pointer along here, as it is pinned
	// for the duration of this call
	foreign := C.ForeignBytes{
		len:  C.int(len(b)),
		data: (*C.uchar)(unsafe.Pointer(&b[0])),
	}

	return rustCall(func(status *C.RustCallStatus) C.RustBuffer {
		return C.ffi_iroh_ffi_rustbuffer_from_bytes(foreign, status)
	})
}

type BufLifter[GoType any] interface {
	Lift(value RustBufferI) GoType
}

type BufLowerer[GoType any] interface {
	Lower(value GoType) C.RustBuffer
}

type BufReader[GoType any] interface {
	Read(reader io.Reader) GoType
}

type BufWriter[GoType any] interface {
	Write(writer io.Writer, value GoType)
}

func LowerIntoRustBuffer[GoType any](bufWriter BufWriter[GoType], value GoType) C.RustBuffer {
	// This might be not the most efficient way but it does not require knowing allocation size
	// beforehand
	var buffer bytes.Buffer
	bufWriter.Write(&buffer, value)

	bytes, err := io.ReadAll(&buffer)
	if err != nil {
		panic(fmt.Errorf("reading written data: %w", err))
	}
	return bytesToRustBuffer(bytes)
}

func LiftFromRustBuffer[GoType any](bufReader BufReader[GoType], rbuf RustBufferI) GoType {
	defer rbuf.Free()
	reader := rbuf.AsReader()
	item := bufReader.Read(reader)
	if reader.Len() > 0 {
		// TODO: Remove this
		leftover, _ := io.ReadAll(reader)
		panic(fmt.Errorf("Junk remaining in buffer after lifting: %s", string(leftover)))
	}
	return item
}

func rustCallWithError[E any, U any](converter BufReader[E], callback func(*C.RustCallStatus) U) (U, E) {
	var status C.RustCallStatus
	returnValue := callback(&status)
	err := checkCallStatus(converter, status)
	return returnValue, err
}

func checkCallStatus[E any](converter BufReader[E], status C.RustCallStatus) E {
	switch status.code {
	case 0:
		var zero E
		return zero
	case 1:
		return LiftFromRustBuffer(converter, GoRustBuffer{inner: status.errorBuf})
	case 2:
		// when the rust code sees a panic, it tries to construct a rustBuffer
		// with the message.  but if that code panics, then it just sends back
		// an empty buffer.
		if status.errorBuf.len > 0 {
			panic(fmt.Errorf("%s", FfiConverterStringINSTANCE.Lift(GoRustBuffer{inner: status.errorBuf})))
		} else {
			panic(fmt.Errorf("Rust panicked while handling Rust panic"))
		}
	default:
		panic(fmt.Errorf("unknown status code: %d", status.code))
	}
}

func checkCallStatusUnknown(status C.RustCallStatus) error {
	switch status.code {
	case 0:
		return nil
	case 1:
		panic(fmt.Errorf("function not returning an error returned an error"))
	case 2:
		// when the rust code sees a panic, it tries to construct a C.RustBuffer
		// with the message.  but if that code panics, then it just sends back
		// an empty buffer.
		if status.errorBuf.len > 0 {
			panic(fmt.Errorf("%s", FfiConverterStringINSTANCE.Lift(GoRustBuffer{
				inner: status.errorBuf,
			})))
		} else {
			panic(fmt.Errorf("Rust panicked while handling Rust panic"))
		}
	default:
		return fmt.Errorf("unknown status code: %d", status.code)
	}
}

func rustCall[U any](callback func(*C.RustCallStatus) U) U {
	returnValue, err := rustCallWithError[error](nil, callback)
	if err != nil {
		panic(err)
	}
	return returnValue
}

type NativeError interface {
	AsError() error
}

func writeInt8(writer io.Writer, value int8) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint8(writer io.Writer, value uint8) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt16(writer io.Writer, value int16) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint16(writer io.Writer, value uint16) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt32(writer io.Writer, value int32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint32(writer io.Writer, value uint32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeInt64(writer io.Writer, value int64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeUint64(writer io.Writer, value uint64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeFloat32(writer io.Writer, value float32) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func writeFloat64(writer io.Writer, value float64) {
	if err := binary.Write(writer, binary.BigEndian, value); err != nil {
		panic(err)
	}
}

func readInt8(reader io.Reader) int8 {
	var result int8
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint8(reader io.Reader) uint8 {
	var result uint8
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt16(reader io.Reader) int16 {
	var result int16
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint16(reader io.Reader) uint16 {
	var result uint16
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt32(reader io.Reader) int32 {
	var result int32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint32(reader io.Reader) uint32 {
	var result uint32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readInt64(reader io.Reader) int64 {
	var result int64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readUint64(reader io.Reader) uint64 {
	var result uint64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readFloat32(reader io.Reader) float32 {
	var result float32
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func readFloat64(reader io.Reader) float64 {
	var result float64
	if err := binary.Read(reader, binary.BigEndian, &result); err != nil {
		panic(err)
	}
	return result
}

func init() {

	FfiConverterAddrChangeCallbackINSTANCE.register()
	FfiConverterHomeRelayCallbackINSTANCE.register()
	FfiConverterNetworkChangeCallbackINSTANCE.register()
	FfiConverterPathChangeCallbackINSTANCE.register()
	FfiConverterPathEventCallbackINSTANCE.register()
	FfiConverterPresetINSTANCE.register()
	FfiConverterProtocolCreatorINSTANCE.register()
	FfiConverterProtocolHandlerINSTANCE.register()
	uniffiCheckChecksums()
}

func uniffiCheckChecksums() {
	// Get the bindings contract version from our ComponentInterface
	bindingsContractVersion := 30
	// Get the scaffolding contract version by calling the into the dylib
	scaffoldingContractVersion := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.ffi_iroh_ffi_uniffi_contract_version()
	})
	if bindingsContractVersion != int(scaffoldingContractVersion) {
		// If this happens try cleaning and rebuilding your project
		panic("iroh_ffi: UniFFI contract version mismatch")
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_func_set_log_level()
		})
		if checksum != 52619 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_func_set_log_level: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_func_preset_minimal()
		})
		if checksum != 1543 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_func_preset_minimal: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_func_preset_n0()
		})
		if checksum != 14809 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_func_preset_n0: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_func_preset_n0_disable_relay()
		})
		if checksum != 45395 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_func_preset_n0_disable_relay: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_accepting_alpn()
		})
		if checksum != 1935 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_accepting_alpn: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_accepting_connect()
		})
		if checksum != 4822 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_accepting_connect: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connecting_alpn()
		})
		if checksum != 43012 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connecting_alpn: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connecting_connect()
		})
		if checksum != 18409 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connecting_connect: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connecting_remote_id()
		})
		if checksum != 20505 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connecting_remote_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_incoming_accept()
		})
		if checksum != 17830 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_incoming_accept: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_incoming_ignore()
		})
		if checksum != 3710 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_incoming_ignore: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_incoming_local_addr()
		})
		if checksum != 49615 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_incoming_local_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_incoming_refuse()
		})
		if checksum != 32144 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_incoming_refuse: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_incoming_remote_addr()
		})
		if checksum != 41074 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_incoming_remote_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_incoming_remote_addr_validated()
		})
		if checksum != 11914 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_incoming_remote_addr_validated: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_incoming_retry()
		})
		if checksum != 5830 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_incoming_retry: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_bistream_recv()
		})
		if checksum != 64172 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_bistream_recv: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_bistream_send()
		})
		if checksum != 17421 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_bistream_send: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_accept_bi()
		})
		if checksum != 24717 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_accept_bi: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_accept_uni()
		})
		if checksum != 14498 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_accept_uni: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_alpn()
		})
		if checksum != 24307 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_alpn: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_close()
		})
		if checksum != 4437 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_close: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_close_reason()
		})
		if checksum != 54740 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_close_reason: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_closed()
		})
		if checksum != 47559 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_closed: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_datagram_send_buffer_space()
		})
		if checksum != 43524 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_datagram_send_buffer_space: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_max_datagram_size()
		})
		if checksum != 57931 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_max_datagram_size: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_open_bi()
		})
		if checksum != 7884 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_open_bi: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_open_uni()
		})
		if checksum != 45141 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_open_uni: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_paths()
		})
		if checksum != 29415 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_paths: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_read_datagram()
		})
		if checksum != 7530 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_read_datagram: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_remote_id()
		})
		if checksum != 30142 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_remote_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_rtt()
		})
		if checksum != 43391 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_rtt: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_send_datagram()
		})
		if checksum != 28522 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_send_datagram: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_send_datagram_wait()
		})
		if checksum != 48924 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_send_datagram_wait: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_set_max_concurrent_bi_streams()
		})
		if checksum != 50250 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_set_max_concurrent_bi_streams: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_set_max_concurrent_uni_streams()
		})
		if checksum != 26941 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_set_max_concurrent_uni_streams: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_set_receive_window()
		})
		if checksum != 49973 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_set_receive_window: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_side()
		})
		if checksum != 41532 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_side: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_stable_id()
		})
		if checksum != 47103 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_stable_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_stats()
		})
		if checksum != 46010 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_stats: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_watch_path_events()
		})
		if checksum != 61648 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_watch_path_events: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_connection_watch_paths()
		})
		if checksum != 12199 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_connection_watch_paths: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_accept_next()
		})
		if checksum != 38336 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_accept_next: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_add_external_addr()
		})
		if checksum != 65222 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_add_external_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_addr()
		})
		if checksum != 25271 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_bound_sockets()
		})
		if checksum != 64249 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_bound_sockets: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_close()
		})
		if checksum != 8483 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_close: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_connect()
		})
		if checksum != 28652 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_connect: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_connect_pending()
		})
		if checksum != 15705 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_connect_pending: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_id()
		})
		if checksum != 21819 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_insert_relay()
		})
		if checksum != 12359 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_insert_relay: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_is_closed()
		})
		if checksum != 32495 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_is_closed: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_online()
		})
		if checksum != 27176 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_online: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_remote_addr()
		})
		if checksum != 28984 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_remote_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_remove_external_addr()
		})
		if checksum != 28593 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_remove_external_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_remove_relay()
		})
		if checksum != 27801 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_remove_relay: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_secret_key()
		})
		if checksum != 53232 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_secret_key: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_set_alpns()
		})
		if checksum != 19499 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_set_alpns: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_stats()
		})
		if checksum != 39646 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_stats: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_watch_addr()
		})
		if checksum != 33560 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_watch_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_watch_home_relay()
		})
		if checksum != 61148 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_watch_home_relay: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpoint_watch_network_change()
		})
		if checksum != 28710 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpoint_watch_network_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_alpns()
		})
		if checksum != 55626 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_alpns: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_apply_minimal()
		})
		if checksum != 3398 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_apply_minimal: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_apply_n0()
		})
		if checksum != 29986 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_apply_n0: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_apply_n0_disable_relay()
		})
		if checksum != 20494 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_apply_n0_disable_relay: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_bind()
		})
		if checksum != 5850 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_bind: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_bind_addr()
		})
		if checksum != 50528 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_bind_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_relay_mode()
		})
		if checksum != 17405 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_relay_mode: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointbuilder_secret_key()
		})
		if checksum != 35604 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointbuilder_secret_key: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_preset_apply()
		})
		if checksum != 64281 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_preset_apply: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_protocolcreator_create()
		})
		if checksum != 61404 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_protocolcreator_create: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_protocolhandler_accept()
		})
		if checksum != 47317 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_protocolhandler_accept: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_protocolhandler_shutdown()
		})
		if checksum != 18593 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_protocolhandler_shutdown: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_recvstream_bytes_read()
		})
		if checksum != 16585 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_recvstream_bytes_read: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_recvstream_id()
		})
		if checksum != 36775 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_recvstream_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_recvstream_read()
		})
		if checksum != 19153 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_recvstream_read: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_recvstream_read_exact()
		})
		if checksum != 6617 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_recvstream_read_exact: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_recvstream_read_to_end()
		})
		if checksum != 10448 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_recvstream_read_to_end: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_recvstream_received_reset()
		})
		if checksum != 7090 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_recvstream_received_reset: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_recvstream_stop()
		})
		if checksum != 6249 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_recvstream_stop: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_finish()
		})
		if checksum != 43289 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_finish: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_id()
		})
		if checksum != 21729 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_priority()
		})
		if checksum != 120 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_priority: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_reset()
		})
		if checksum != 6025 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_reset: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_set_priority()
		})
		if checksum != 18123 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_set_priority: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_stopped()
		})
		if checksum != 18559 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_stopped: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_write()
		})
		if checksum != 30292 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_write: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_sendstream_write_all()
		})
		if checksum != 64390 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_sendstream_write_all: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_iroherror_debug_message()
		})
		if checksum != 33751 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_iroherror_debug_message: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_iroherror_is_kind()
		})
		if checksum != 10479 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_iroherror_is_kind: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_iroherror_kind()
		})
		if checksum != 11512 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_iroherror_kind: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_iroherror_message()
		})
		if checksum != 60838 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_iroherror_message: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointid_fmt_short()
		})
		if checksum != 41579 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointid_fmt_short: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointid_to_bytes()
		})
		if checksum != 28032 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointid_to_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointid_verify()
		})
		if checksum != 26422 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointid_verify: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_secretkey_public()
		})
		if checksum != 4129 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_secretkey_public: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_secretkey_sign()
		})
		if checksum != 46972 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_secretkey_sign: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_secretkey_to_bytes()
		})
		if checksum != 48409 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_secretkey_to_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_signature_to_bytes()
		})
		if checksum != 39387 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_signature_to_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointaddr_direct_addresses()
		})
		if checksum != 63199 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointaddr_direct_addresses: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointaddr_id()
		})
		if checksum != 32503 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointaddr_id: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointaddr_relay_url()
		})
		if checksum != 24207 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointaddr_relay_url: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_pathchangecallback_on_change()
		})
		if checksum != 24759 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_pathchangecallback_on_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_patheventcallback_on_event()
		})
		if checksum != 26147 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_patheventcallback_on_event: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymap_contains()
		})
		if checksum != 54079 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymap_contains: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymap_get()
		})
		if checksum != 6837 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymap_get: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymap_insert()
		})
		if checksum != 45587 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymap_insert: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymap_is_empty()
		})
		if checksum != 18319 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymap_is_empty: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymap_len()
		})
		if checksum != 54399 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymap_len: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymap_remove()
		})
		if checksum != 22144 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymap_remove: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymap_urls()
		})
		if checksum != 732 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymap_urls: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_relaymode_relay_map()
		})
		if checksum != 38538 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_relaymode_relay_map: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_servicesclient_name()
		})
		if checksum != 37977 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_servicesclient_name: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_servicesclient_ping()
		})
		if checksum != 52471 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_servicesclient_ping: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_servicesclient_push_metrics()
		})
		if checksum != 19984 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_servicesclient_push_metrics: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_servicesclient_set_name()
		})
		if checksum != 56201 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_servicesclient_set_name: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_servicesclient_submit_network_diagnostics()
		})
		if checksum != 36167 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_servicesclient_submit_network_diagnostics: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_endpointticket_endpoint_addr()
		})
		if checksum != 64212 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_endpointticket_endpoint_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_addrchangecallback_on_change()
		})
		if checksum != 35709 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_addrchangecallback_on_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_homerelaycallback_on_change()
		})
		if checksum != 24411 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_homerelaycallback_on_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_networkchangecallback_on_change()
		})
		if checksum != 8251 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_networkchangecallback_on_change: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_method_watchhandle_stop()
		})
		if checksum != 5389 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_method_watchhandle_stop: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_endpoint_bind()
		})
		if checksum != 33964 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_endpoint_bind: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_endpointbuilder_new()
		})
		if checksum != 16347 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_endpointbuilder_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_endpointid_from_bytes()
		})
		if checksum != 63462 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_endpointid_from_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_endpointid_from_string()
		})
		if checksum != 47236 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_endpointid_from_string: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_secretkey_from_bytes()
		})
		if checksum != 61009 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_secretkey_from_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_secretkey_generate()
		})
		if checksum != 56581 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_secretkey_generate: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_signature_from_bytes()
		})
		if checksum != 62207 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_signature_from_bytes: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_endpointaddr_new()
		})
		if checksum != 40386 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_endpointaddr_new: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_relaymap_empty()
		})
		if checksum != 49514 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_relaymap_empty: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_relaymap_from_urls()
		})
		if checksum != 678 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_relaymap_from_urls: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_relaymode_custom()
		})
		if checksum != 51396 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_relaymode_custom: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_relaymode_custom_from_urls()
		})
		if checksum != 49579 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_relaymode_custom_from_urls: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_relaymode_default_mode()
		})
		if checksum != 17157 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_relaymode_default_mode: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_relaymode_disabled()
		})
		if checksum != 42542 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_relaymode_disabled: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_relaymode_staging()
		})
		if checksum != 32490 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_relaymode_staging: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_servicesclient_create()
		})
		if checksum != 11042 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_servicesclient_create: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_endpointticket_from_addr()
		})
		if checksum != 28196 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_endpointticket_from_addr: UniFFI API checksum mismatch")
		}
	}
	{
		checksum := rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint16_t {
			return C.uniffi_iroh_ffi_checksum_constructor_endpointticket_from_string()
		})
		if checksum != 3825 {
			// If this happens try cleaning and rebuilding your project
			panic("iroh_ffi: uniffi_iroh_ffi_checksum_constructor_endpointticket_from_string: UniFFI API checksum mismatch")
		}
	}
}

type FfiConverterUint16 struct{}

var FfiConverterUint16INSTANCE = FfiConverterUint16{}

func (FfiConverterUint16) Lower(value uint16) C.uint16_t {
	return C.uint16_t(value)
}

func (FfiConverterUint16) Write(writer io.Writer, value uint16) {
	writeUint16(writer, value)
}

func (FfiConverterUint16) Lift(value C.uint16_t) uint16 {
	return uint16(value)
}

func (FfiConverterUint16) Read(reader io.Reader) uint16 {
	return readUint16(reader)
}

type FfiDestroyerUint16 struct{}

func (FfiDestroyerUint16) Destroy(_ uint16) {}

type FfiConverterUint32 struct{}

var FfiConverterUint32INSTANCE = FfiConverterUint32{}

func (FfiConverterUint32) Lower(value uint32) C.uint32_t {
	return C.uint32_t(value)
}

func (FfiConverterUint32) Write(writer io.Writer, value uint32) {
	writeUint32(writer, value)
}

func (FfiConverterUint32) Lift(value C.uint32_t) uint32 {
	return uint32(value)
}

func (FfiConverterUint32) Read(reader io.Reader) uint32 {
	return readUint32(reader)
}

type FfiDestroyerUint32 struct{}

func (FfiDestroyerUint32) Destroy(_ uint32) {}

type FfiConverterInt32 struct{}

var FfiConverterInt32INSTANCE = FfiConverterInt32{}

func (FfiConverterInt32) Lower(value int32) C.int32_t {
	return C.int32_t(value)
}

func (FfiConverterInt32) Write(writer io.Writer, value int32) {
	writeInt32(writer, value)
}

func (FfiConverterInt32) Lift(value C.int32_t) int32 {
	return int32(value)
}

func (FfiConverterInt32) Read(reader io.Reader) int32 {
	return readInt32(reader)
}

type FfiDestroyerInt32 struct{}

func (FfiDestroyerInt32) Destroy(_ int32) {}

type FfiConverterUint64 struct{}

var FfiConverterUint64INSTANCE = FfiConverterUint64{}

func (FfiConverterUint64) Lower(value uint64) C.uint64_t {
	return C.uint64_t(value)
}

func (FfiConverterUint64) Write(writer io.Writer, value uint64) {
	writeUint64(writer, value)
}

func (FfiConverterUint64) Lift(value C.uint64_t) uint64 {
	return uint64(value)
}

func (FfiConverterUint64) Read(reader io.Reader) uint64 {
	return readUint64(reader)
}

type FfiDestroyerUint64 struct{}

func (FfiDestroyerUint64) Destroy(_ uint64) {}

type FfiConverterInt64 struct{}

var FfiConverterInt64INSTANCE = FfiConverterInt64{}

func (FfiConverterInt64) Lower(value int64) C.int64_t {
	return C.int64_t(value)
}

func (FfiConverterInt64) Write(writer io.Writer, value int64) {
	writeInt64(writer, value)
}

func (FfiConverterInt64) Lift(value C.int64_t) int64 {
	return int64(value)
}

func (FfiConverterInt64) Read(reader io.Reader) int64 {
	return readInt64(reader)
}

type FfiDestroyerInt64 struct{}

func (FfiDestroyerInt64) Destroy(_ int64) {}

type FfiConverterBool struct{}

var FfiConverterBoolINSTANCE = FfiConverterBool{}

func (FfiConverterBool) Lower(value bool) C.int8_t {
	if value {
		return C.int8_t(1)
	}
	return C.int8_t(0)
}

func (FfiConverterBool) Write(writer io.Writer, value bool) {
	if value {
		writeInt8(writer, 1)
	} else {
		writeInt8(writer, 0)
	}
}

func (FfiConverterBool) Lift(value C.int8_t) bool {
	return value != 0
}

func (FfiConverterBool) Read(reader io.Reader) bool {
	return readInt8(reader) != 0
}

type FfiDestroyerBool struct{}

func (FfiDestroyerBool) Destroy(_ bool) {}

type FfiConverterString struct{}

var FfiConverterStringINSTANCE = FfiConverterString{}

func (FfiConverterString) Lift(rb RustBufferI) string {
	defer rb.Free()
	reader := rb.AsReader()
	b, err := io.ReadAll(reader)
	if err != nil {
		panic(fmt.Errorf("reading reader: %w", err))
	}
	return string(b)
}

func (FfiConverterString) Read(reader io.Reader) string {
	length := readInt32(reader)
	buffer := make([]byte, length)
	read_length, err := reader.Read(buffer)
	if err != nil && err != io.EOF {
		panic(err)
	}
	if read_length != int(length) {
		panic(fmt.Errorf("bad read length when reading string, expected %d, read %d", length, read_length))
	}
	return string(buffer)
}

func (FfiConverterString) Lower(value string) C.RustBuffer {
	return stringToRustBuffer(value)
}

func (c FfiConverterString) LowerExternal(value string) ExternalCRustBuffer {
	return RustBufferFromC(stringToRustBuffer(value))
}

func (FfiConverterString) Write(writer io.Writer, value string) {
	if len(value) > math.MaxInt32 {
		panic("String is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	write_length, err := io.WriteString(writer, value)
	if err != nil {
		panic(err)
	}
	if write_length != len(value) {
		panic(fmt.Errorf("bad write length when writing string, expected %d, written %d", len(value), write_length))
	}
}

type FfiDestroyerString struct{}

func (FfiDestroyerString) Destroy(_ string) {}

type FfiConverterBytes struct{}

var FfiConverterBytesINSTANCE = FfiConverterBytes{}

func (c FfiConverterBytes) Lower(value []byte) C.RustBuffer {
	return LowerIntoRustBuffer[[]byte](c, value)
}

func (c FfiConverterBytes) LowerExternal(value []byte) ExternalCRustBuffer {
	return RustBufferFromC(c.Lower(value))
}

func (c FfiConverterBytes) Write(writer io.Writer, value []byte) {
	if len(value) > math.MaxInt32 {
		panic("[]byte is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	write_length, err := writer.Write(value)
	if err != nil {
		panic(err)
	}
	if write_length != len(value) {
		panic(fmt.Errorf("bad write length when writing []byte, expected %d, written %d", len(value), write_length))
	}
}

func (c FfiConverterBytes) Lift(rb RustBufferI) []byte {
	return LiftFromRustBuffer[[]byte](c, rb)
}

func (c FfiConverterBytes) Read(reader io.Reader) []byte {
	length := readInt32(reader)
	buffer := make([]byte, length)
	read_length, err := reader.Read(buffer)
	if err != nil && err != io.EOF {
		panic(err)
	}
	if read_length != int(length) {
		panic(fmt.Errorf("bad read length when reading []byte, expected %d, read %d", length, read_length))
	}
	return buffer
}

type FfiDestroyerBytes struct{}

func (FfiDestroyerBytes) Destroy(_ []byte) {}

// Below is an implementation of synchronization requirements outlined in the link.
// https://github.com/mozilla/uniffi-rs/blob/0dc031132d9493ca812c3af6e7dd60ad2ea95bf0/uniffi_bindgen/src/bindings/kotlin/templates/ObjectRuntime.kt#L31

type FfiObject struct {
	handle        C.uint64_t
	callCounter   atomic.Int64
	cloneFunction func(C.uint64_t, *C.RustCallStatus) C.uint64_t
	freeFunction  func(C.uint64_t, *C.RustCallStatus)
	destroyed     atomic.Bool
}

func newFfiObject(
	handle C.uint64_t,
	cloneFunction func(C.uint64_t, *C.RustCallStatus) C.uint64_t,
	freeFunction func(C.uint64_t, *C.RustCallStatus),
) FfiObject {
	return FfiObject{
		handle:        handle,
		cloneFunction: cloneFunction,
		freeFunction:  freeFunction,
	}
}

func (ffiObject *FfiObject) incrementPointer(debugName string) C.uint64_t {
	for {
		counter := ffiObject.callCounter.Load()
		if counter <= -1 {
			panic(fmt.Errorf("%v object has already been destroyed", debugName))
		}
		if counter == math.MaxInt64 {
			panic(fmt.Errorf("%v object call counter would overflow", debugName))
		}
		if ffiObject.callCounter.CompareAndSwap(counter, counter+1) {
			break
		}
	}

	return rustCall(func(status *C.RustCallStatus) C.uint64_t {
		return ffiObject.cloneFunction(ffiObject.handle, status)
	})
}

func (ffiObject *FfiObject) decrementPointer() {
	if ffiObject.callCounter.Add(-1) == -1 {
		ffiObject.freeRustArcPtr()
	}
}

func (ffiObject *FfiObject) destroy() {
	if ffiObject.destroyed.CompareAndSwap(false, true) {
		if ffiObject.callCounter.Add(-1) == -1 {
			ffiObject.freeRustArcPtr()
		}
	}
}

func (ffiObject *FfiObject) freeRustArcPtr() {
	if ffiObject.handle == 0 {
		return
	}
	rustCall(func(status *C.RustCallStatus) int32 {
		ffiObject.freeFunction(ffiObject.handle, status)
		return 0
	})
}

// A server-side handshake in progress. Await with [`Self::connect`].
type AcceptingInterface interface {
	// Read the ALPN protocol from the peer's handshake data (resolves once
	// the ClientHello has been received).
	Alpn() ([]byte, error)
	// Wait for the handshake to complete, producing a [`Connection`].
	Connect() (*Connection, error)
}

// A server-side handshake in progress. Await with [`Self::connect`].
type Accepting struct {
	ffiObject FfiObject
}

// Read the ALPN protocol from the peer's handshake data (resolves once
// the ClientHello has been received).
func (_self *Accepting) Alpn() ([]byte, error) {
	_pointer := _self.ffiObject.incrementPointer("*Accepting")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) []byte {
			return FfiConverterBytesINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_accepting_alpn(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Wait for the handshake to complete, producing a [`Connection`].
func (_self *Accepting) Connect() (*Connection, error) {
	_pointer := _self.ffiObject.incrementPointer("*Accepting")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *Connection {
			return FfiConverterConnectionINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_accepting_connect(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}
func (object *Accepting) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterAccepting struct{}

var FfiConverterAcceptingINSTANCE = FfiConverterAccepting{}

func (c FfiConverterAccepting) Lift(handle C.uint64_t) *Accepting {
	result := &Accepting{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_accepting(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_accepting(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Accepting).Destroy)
	return result
}

func (c FfiConverterAccepting) Read(reader io.Reader) *Accepting {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterAccepting) Lower(value *Accepting) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Accepting")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterAccepting) Write(writer io.Writer, value *Accepting) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalAccepting(handle uint64) *Accepting {
	return FfiConverterAcceptingINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalAccepting(value *Accepting) uint64 {
	return uint64(FfiConverterAcceptingINSTANCE.Lower(value))
}

type FfiDestroyerAccepting struct{}

func (_ FfiDestroyerAccepting) Destroy(value *Accepting) {
	value.Destroy()
}

// Callback invoked whenever the endpoint's [`EndpointAddr`] changes.
type AddrChangeCallback interface {
	OnChange(addr *EndpointAddr) error
}

// Callback invoked whenever the endpoint's [`EndpointAddr`] changes.
type AddrChangeCallbackImpl struct {
	ffiObject FfiObject
}

func (_self *AddrChangeCallbackImpl) OnChange(addr *EndpointAddr) error {
	_pointer := _self.ffiObject.incrementPointer("AddrChangeCallback")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*CallbackError](
		FfiConverterCallbackErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_addrchangecallback_on_change(
			_pointer, FfiConverterEndpointAddrINSTANCE.Lower(addr)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *AddrChangeCallbackImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterAddrChangeCallback struct {
	handleMap *concurrentHandleMap[AddrChangeCallback]
}

var FfiConverterAddrChangeCallbackINSTANCE = FfiConverterAddrChangeCallback{
	handleMap: newConcurrentHandleMap[AddrChangeCallback](),
}

func (c FfiConverterAddrChangeCallback) Lift(handle C.uint64_t) AddrChangeCallback {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &AddrChangeCallbackImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_addrchangecallback(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_addrchangecallback(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*AddrChangeCallbackImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterAddrChangeCallback) Read(reader io.Reader) AddrChangeCallback {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterAddrChangeCallback) Lower(value AddrChangeCallback) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*AddrChangeCallbackImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("AddrChangeCallback")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterAddrChangeCallback) Write(writer io.Writer, value AddrChangeCallback) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalAddrChangeCallback(handle uint64) AddrChangeCallback {
	return FfiConverterAddrChangeCallbackINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalAddrChangeCallback(value AddrChangeCallback) uint64 {
	return uint64(FfiConverterAddrChangeCallbackINSTANCE.Lower(value))
}

type FfiDestroyerAddrChangeCallback struct{}

func (_ FfiDestroyerAddrChangeCallback) Destroy(value AddrChangeCallback) {
	if val, ok := value.(*AddrChangeCallbackImpl); ok {
		val.Destroy()
	}
}

type uniffiCallbackResult C.int8_t

const (
	uniffiIdxCallbackFree               uniffiCallbackResult = 0
	uniffiCallbackResultSuccess         uniffiCallbackResult = 0
	uniffiCallbackResultError           uniffiCallbackResult = 1
	uniffiCallbackUnexpectedResultError uniffiCallbackResult = 2
	uniffiCallbackCancelled             uniffiCallbackResult = 3
)

type concurrentHandleMap[T any] struct {
	handles       map[uint64]T
	currentHandle uint64
	lock          sync.RWMutex
}

func newConcurrentHandleMap[T any]() *concurrentHandleMap[T] {
	return &concurrentHandleMap[T]{
		handles:       map[uint64]T{},
		currentHandle: 1,
	}
}

func (cm *concurrentHandleMap[T]) insert(obj T) uint64 {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	handle := cm.currentHandle
	cm.currentHandle = cm.currentHandle + 2
	cm.handles[handle] = obj
	return handle
}

func (cm *concurrentHandleMap[T]) remove(handle uint64) {
	cm.lock.Lock()
	defer cm.lock.Unlock()

	delete(cm.handles, handle)
}

func (cm *concurrentHandleMap[T]) tryGet(handle uint64) (T, bool) {
	cm.lock.RLock()
	defer cm.lock.RUnlock()

	val, ok := cm.handles[handle]
	return val, ok
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackMethod0
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackMethod0(uniffiHandle C.uint64_t, addr C.uint64_t, uniffiFutureCallback C.UniffiForeignFutureCompleteVoid, uniffiCallbackData C.uint64_t, uniffiOutDroppedCallback *C.UniffiForeignFutureDroppedCallbackStruct) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterAddrChangeCallbackINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	result := make(chan C.UniffiForeignFutureResultVoid, 1)
	cancel := make(chan struct{}, 1)
	guardHandle := cgo.NewHandle(cancel)
	*uniffiOutDroppedCallback = C.UniffiForeignFutureDroppedCallbackStruct{
		handle: C.uint64_t(guardHandle),
		free:   C.UniffiForeignFutureDroppedCallback(C.iroh_uniffiFreeGorutine),
	}

	// Wait for compleation or cancel
	go func() {
		select {
		case <-cancel:
		case res := <-result:
			C.call_UniffiForeignFutureCompleteVoid(uniffiFutureCallback, uniffiCallbackData, res)
		}
	}()

	// Eval callback asynchroniously
	go func() {
		asyncResult := &C.UniffiForeignFutureResultVoid{}
		callStatus := &asyncResult.callStatus
		defer func() {
			result <- *asyncResult
		}()

		err :=
			uniffiObj.OnChange(
				FfiConverterEndpointAddrINSTANCE.Lift(addr),
			)

		if err != nil {
			var actualError *CallbackError
			if errors.As(err, &actualError) {
				*callStatus = C.RustCallStatus{
					code:     C.int8_t(uniffiCallbackResultError),
					errorBuf: FfiConverterCallbackErrorINSTANCE.Lower(actualError),
				}
			} else {
				*callStatus = C.RustCallStatus{
					code: C.int8_t(uniffiCallbackUnexpectedResultError),
				}
			}
			return
		}

	}()
}

var UniffiVTableCallbackInterfaceAddrChangeCallbackINSTANCE = C.UniffiVTableCallbackInterfaceAddrChangeCallback{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackClone),
	onChange:    (C.UniffiCallbackInterfaceAddrChangeCallbackMethod0)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackMethod0),
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackFree
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackFree(handle C.uint64_t) {
	FfiConverterAddrChangeCallbackINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackClone
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceAddrChangeCallbackClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterAddrChangeCallbackINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterAddrChangeCallbackINSTANCE.handleMap.insert(val))
}

func (c FfiConverterAddrChangeCallback) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_addrchangecallback(&UniffiVTableCallbackInterfaceAddrChangeCallbackINSTANCE)
}

// A bidirectional QUIC stream pair.
type BiStreamInterface interface {
	Recv() *RecvStream
	Send() *SendStream
}

// A bidirectional QUIC stream pair.
type BiStream struct {
	ffiObject FfiObject
}

func (_self *BiStream) Recv() *RecvStream {
	_pointer := _self.ffiObject.incrementPointer("*BiStream")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterRecvStreamINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_bistream_recv(
			_pointer, _uniffiStatus)
	}))
}

func (_self *BiStream) Send() *SendStream {
	_pointer := _self.ffiObject.incrementPointer("*BiStream")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSendStreamINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_bistream_send(
			_pointer, _uniffiStatus)
	}))
}
func (object *BiStream) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterBiStream struct{}

var FfiConverterBiStreamINSTANCE = FfiConverterBiStream{}

func (c FfiConverterBiStream) Lift(handle C.uint64_t) *BiStream {
	result := &BiStream{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_bistream(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_bistream(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*BiStream).Destroy)
	return result
}

func (c FfiConverterBiStream) Read(reader io.Reader) *BiStream {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterBiStream) Lower(value *BiStream) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*BiStream")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterBiStream) Write(writer io.Writer, value *BiStream) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalBiStream(handle uint64) *BiStream {
	return FfiConverterBiStreamINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalBiStream(value *BiStream) uint64 {
	return uint64(FfiConverterBiStreamINSTANCE.Lower(value))
}

type FfiDestroyerBiStream struct{}

func (_ FfiDestroyerBiStream) Destroy(value *BiStream) {
	value.Destroy()
}

// A client-side handshake in progress. Await with [`Self::connect`].
type ConnectingInterface interface {
	// Read the ALPN protocol from the peer's handshake data (resolves once
	// the server has responded with its ServerHello).
	Alpn() ([]byte, error)
	// Wait for the handshake to complete, producing a [`Connection`].
	Connect() (*Connection, error)
	// The [`EndpointId`] this connection attempt targets.
	RemoteId() (*EndpointId, error)
}

// A client-side handshake in progress. Await with [`Self::connect`].
type Connecting struct {
	ffiObject FfiObject
}

// Read the ALPN protocol from the peer's handshake data (resolves once
// the server has responded with its ServerHello).
func (_self *Connecting) Alpn() ([]byte, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connecting")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) []byte {
			return FfiConverterBytesINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connecting_alpn(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Wait for the handshake to complete, producing a [`Connection`].
func (_self *Connecting) Connect() (*Connection, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connecting")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *Connection {
			return FfiConverterConnectionINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connecting_connect(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// The [`EndpointId`] this connection attempt targets.
func (_self *Connecting) RemoteId() (*EndpointId, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connecting")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *EndpointId {
			return FfiConverterEndpointIdINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connecting_remote_id(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}
func (object *Connecting) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterConnecting struct{}

var FfiConverterConnectingINSTANCE = FfiConverterConnecting{}

func (c FfiConverterConnecting) Lift(handle C.uint64_t) *Connecting {
	result := &Connecting{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_connecting(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_connecting(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Connecting).Destroy)
	return result
}

func (c FfiConverterConnecting) Read(reader io.Reader) *Connecting {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterConnecting) Lower(value *Connecting) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Connecting")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterConnecting) Write(writer io.Writer, value *Connecting) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalConnecting(handle uint64) *Connecting {
	return FfiConverterConnectingINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalConnecting(value *Connecting) uint64 {
	return uint64(FfiConverterConnectingINSTANCE.Lower(value))
}

type FfiDestroyerConnecting struct{}

func (_ FfiDestroyerConnecting) Destroy(value *Connecting) {
	value.Destroy()
}

// An active QUIC connection to a remote endpoint.
type ConnectionInterface interface {
	// Accept the next incoming bidirectional stream.
	AcceptBi() (*BiStream, error)
	// Accept the next incoming unidirectional stream.
	AcceptUni() (*RecvStream, error)
	// The ALPN protocol negotiated for this connection.
	Alpn() []byte
	// Close the connection immediately with the given application error code.
	//
	// Signed for Kotlin/Swift ergonomics; negative values are rejected.
	Close(errorCode int64, reason []byte) error
	// If the connection is closed, the reason why. None if still open.
	CloseReason() *string
	// Wait for the connection to be closed, returning the cause.
	Closed() string
	// Bytes available in the datagram send buffer.
	DatagramSendBufferSpace() uint64
	// Maximum size of a datagram that can currently be sent.
	MaxDatagramSize() *uint64
	// Open a new bidirectional outgoing stream.
	OpenBi() (*BiStream, error)
	// Open a new unidirectional outgoing stream.
	OpenUni() (*SendStream, error)
	// A snapshot of all currently open network paths for this connection.
	Paths() []PathSnapshot
	// Read the next datagram from the connection.
	ReadDatagram() ([]byte, error)
	// The [`EndpointId`] of the remote peer.
	RemoteId() *EndpointId
	// Current best estimate of this connection's RTT on the selected path,
	// in milliseconds. `None` if no path is currently selected.
	Rtt() *uint64
	// Send a datagram on this connection.
	SendDatagram(data []byte) error
	// Like [`Connection::send_datagram`] but waits for capacity if the send
	// buffer is full.
	SendDatagramWait(data []byte) error
	// Set the maximum number of concurrent incoming bidirectional streams.
	SetMaxConcurrentBiStreams(count uint64) error
	// Set the maximum number of concurrent incoming unidirectional streams.
	SetMaxConcurrentUniStreams(count uint64) error
	// Set the receive window for this connection.
	SetReceiveWindow(count uint64) error
	// Which side of the connection we are (client or server).
	Side() Side
	// A stable identifier for this connection.
	StableId() uint64
	// A flat snapshot of the most useful headline statistics for this connection.
	Stats() ConnectionStats
	// Register a callback that fires for each individual path event (path
	// opened, closed, selected, or lagged).
	WatchPathEvents(callback PathEventCallback) *WatchHandle
	// Register a callback that fires with the current set of open paths
	// whenever the path list (or selected path) changes.
	WatchPaths(callback PathChangeCallback) *WatchHandle
}

// An active QUIC connection to a remote endpoint.
type Connection struct {
	ffiObject FfiObject
}

// Accept the next incoming bidirectional stream.
func (_self *Connection) AcceptBi() (*BiStream, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *BiStream {
			return FfiConverterBiStreamINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connection_accept_bi(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Accept the next incoming unidirectional stream.
func (_self *Connection) AcceptUni() (*RecvStream, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *RecvStream {
			return FfiConverterRecvStreamINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connection_accept_uni(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// The ALPN protocol negotiated for this connection.
func (_self *Connection) Alpn() []byte {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBytesINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_connection_alpn(
				_pointer, _uniffiStatus),
		}
	}))
}

// Close the connection immediately with the given application error code.
//
// Signed for Kotlin/Swift ergonomics; negative values are rejected.
func (_self *Connection) Close(errorCode int64, reason []byte) error {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_connection_close(
			_pointer, FfiConverterInt64INSTANCE.Lower(errorCode), FfiConverterBytesINSTANCE.Lower(reason), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// If the connection is closed, the reason why. None if still open.
func (_self *Connection) CloseReason() *string {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_connection_close_reason(
				_pointer, _uniffiStatus),
		}
	}))
}

// Wait for the connection to be closed, returning the cause.
func (_self *Connection) Closed() string {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	res, _ := uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) string {
			return FfiConverterStringINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connection_closed(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	return res
}

// Bytes available in the datagram send buffer.
func (_self *Connection) DatagramSendBufferSpace() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_connection_datagram_send_buffer_space(
			_pointer, _uniffiStatus)
	}))
}

// Maximum size of a datagram that can currently be sent.
func (_self *Connection) MaxDatagramSize() *uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_connection_max_datagram_size(
				_pointer, _uniffiStatus),
		}
	}))
}

// Open a new bidirectional outgoing stream.
func (_self *Connection) OpenBi() (*BiStream, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *BiStream {
			return FfiConverterBiStreamINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connection_open_bi(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Open a new unidirectional outgoing stream.
func (_self *Connection) OpenUni() (*SendStream, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *SendStream {
			return FfiConverterSendStreamINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connection_open_uni(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// A snapshot of all currently open network paths for this connection.
func (_self *Connection) Paths() []PathSnapshot {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequencePathSnapshotINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_connection_paths(
				_pointer, _uniffiStatus),
		}
	}))
}

// Read the next datagram from the connection.
func (_self *Connection) ReadDatagram() ([]byte, error) {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) []byte {
			return FfiConverterBytesINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_connection_read_datagram(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// The [`EndpointId`] of the remote peer.
func (_self *Connection) RemoteId() *EndpointId {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterEndpointIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_connection_remote_id(
			_pointer, _uniffiStatus)
	}))
}

// Current best estimate of this connection's RTT on the selected path,
// in milliseconds. `None` if no path is currently selected.
func (_self *Connection) Rtt() *uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_connection_rtt(
				_pointer, _uniffiStatus),
		}
	}))
}

// Send a datagram on this connection.
func (_self *Connection) SendDatagram(data []byte) error {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_connection_send_datagram(
			_pointer, FfiConverterBytesINSTANCE.Lower(data), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Like [`Connection::send_datagram`] but waits for capacity if the send
// buffer is full.
func (_self *Connection) SendDatagramWait(data []byte) error {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_connection_send_datagram_wait(
			_pointer, FfiConverterBytesINSTANCE.Lower(data)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// Set the maximum number of concurrent incoming bidirectional streams.
func (_self *Connection) SetMaxConcurrentBiStreams(count uint64) error {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_connection_set_max_concurrent_bi_streams(
			_pointer, FfiConverterUint64INSTANCE.Lower(count), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Set the maximum number of concurrent incoming unidirectional streams.
func (_self *Connection) SetMaxConcurrentUniStreams(count uint64) error {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_connection_set_max_concurrent_uni_streams(
			_pointer, FfiConverterUint64INSTANCE.Lower(count), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Set the receive window for this connection.
func (_self *Connection) SetReceiveWindow(count uint64) error {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_connection_set_receive_window(
			_pointer, FfiConverterUint64INSTANCE.Lower(count), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Which side of the connection we are (client or server).
func (_self *Connection) Side() Side {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSideINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_connection_side(
				_pointer, _uniffiStatus),
		}
	}))
}

// A stable identifier for this connection.
func (_self *Connection) StableId() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_connection_stable_id(
			_pointer, _uniffiStatus)
	}))
}

// A flat snapshot of the most useful headline statistics for this connection.
func (_self *Connection) Stats() ConnectionStats {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterConnectionStatsINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_connection_stats(
				_pointer, _uniffiStatus),
		}
	}))
}

// Register a callback that fires for each individual path event (path
// opened, closed, selected, or lagged).
func (_self *Connection) WatchPathEvents(callback PathEventCallback) *WatchHandle {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterWatchHandleINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_connection_watch_path_events(
			_pointer, FfiConverterPathEventCallbackINSTANCE.Lower(callback), _uniffiStatus)
	}))
}

// Register a callback that fires with the current set of open paths
// whenever the path list (or selected path) changes.
func (_self *Connection) WatchPaths(callback PathChangeCallback) *WatchHandle {
	_pointer := _self.ffiObject.incrementPointer("*Connection")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterWatchHandleINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_connection_watch_paths(
			_pointer, FfiConverterPathChangeCallbackINSTANCE.Lower(callback), _uniffiStatus)
	}))
}
func (object *Connection) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterConnection struct{}

var FfiConverterConnectionINSTANCE = FfiConverterConnection{}

func (c FfiConverterConnection) Lift(handle C.uint64_t) *Connection {
	result := &Connection{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_connection(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_connection(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Connection).Destroy)
	return result
}

func (c FfiConverterConnection) Read(reader io.Reader) *Connection {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterConnection) Lower(value *Connection) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Connection")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterConnection) Write(writer io.Writer, value *Connection) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalConnection(handle uint64) *Connection {
	return FfiConverterConnectionINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalConnection(value *Connection) uint64 {
	return uint64(FfiConverterConnectionINSTANCE.Lower(value))
}

type FfiDestroyerConnection struct{}

func (_ FfiDestroyerConnection) Destroy(value *Connection) {
	value.Destroy()
}

// An iroh endpoint.
//
// Bind one with [`Endpoint::bind`]. Provide protocol handlers via
// [`EndpointOptions::protocols`] to dispatch incoming connections.
type EndpointInterface interface {
	// Pull the next incoming connection attempt from the accept queue.
	//
	// Returns `None` once the endpoint is closed. Use this for a custom accept
	// loop instead of (or in addition to) registering protocol handlers via
	// [`EndpointOptions::protocols`].
	AcceptNext() **Incoming
	// Add an external (manually-known) socket address that this endpoint is
	// reachable on. Useful when running behind a static NAT / load balancer.
	AddExternalAddr(addr string) error
	// The [`EndpointAddr`] for this endpoint (id + currently known addresses).
	Addr() *EndpointAddr
	// The local socket addresses this endpoint is bound to.
	BoundSockets() []string
	// Shut down the endpoint (and, if present, the protocol router).
	Close() error
	// Connect to a remote endpoint via the given ALPN.
	Connect(addr *EndpointAddr, alpn []byte) (*Connection, error)
	// Begin a connection attempt to `addr` for `alpn`, returning the
	// in-progress [`Connecting`] state.
	//
	// Unlike [`Self::connect`], which awaits the handshake before returning,
	// this exposes the pre-handshake handle so the caller can inspect ALPN or
	// drop the attempt explicitly.
	ConnectPending(addr *EndpointAddr, alpn []byte) (*Connecting, error)
	// The [`EndpointId`] of this endpoint.
	Id() *EndpointId
	// Insert (or replace) a relay configuration at runtime.
	InsertRelay(config RelayConfig) error
	// Returns true if the endpoint has been closed.
	IsClosed() bool
	// Resolves once the endpoint has a usable home relay.
	Online()
	// Look up cached information about a remote endpoint, if any.
	RemoteAddr(id *EndpointId) **EndpointAddr
	// Remove a previously-added external address. Returns true if an entry was
	// removed.
	RemoveExternalAddr(addr string) (bool, error)
	// Remove a relay configuration at runtime. Returns true if a relay was
	// removed.
	RemoveRelay(url string) (bool, error)
	// The [`SecretKey`] backing this endpoint's identity.
	SecretKey() *SecretKey
	// Replace the set of ALPNs advertised by this endpoint.
	SetAlpns(alpns [][]byte)
	// Get current statistics for this endpoint.
	//
	// Keys are `"<group>:<metric>"`. Counter / gauge values are saturating-cast to `u32`.
	Stats() map[string]CounterStats
	// Register a callback that fires whenever the endpoint's [`EndpointAddr`]
	// changes (relay home rotates, IP discovered, etc.). The returned
	// [`WatchHandle`] cancels the watcher when dropped or when its `stop()`
	// method is called.
	WatchAddr(callback AddrChangeCallback) *WatchHandle
	// Register a callback that fires whenever the list of relays this endpoint
	// is currently connected to changes.
	WatchHomeRelay(callback HomeRelayCallback) *WatchHandle
	// Register a callback that fires every time the underlying network stack
	// reports a change (interface up/down, NAT change, roaming, etc.).
	WatchNetworkChange(callback NetworkChangeCallback) *WatchHandle
}

// An iroh endpoint.
//
// Bind one with [`Endpoint::bind`]. Provide protocol handlers via
// [`EndpointOptions::protocols`] to dispatch incoming connections.
type Endpoint struct {
	ffiObject FfiObject
}

// Bind a new endpoint with the given options.
func EndpointBind(options EndpointOptions) (*Endpoint, error) {
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *Endpoint {
			return FfiConverterEndpointINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_constructor_endpoint_bind(FfiConverterEndpointOptionsINSTANCE.Lower(options)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Pull the next incoming connection attempt from the accept queue.
//
// Returns `None` once the endpoint is closed. Use this for a custom accept
// loop instead of (or in addition to) registering protocol handlers via
// [`EndpointOptions::protocols`].
func (_self *Endpoint) AcceptNext() **Incoming {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	res, _ := uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) **Incoming {
			return FfiConverterOptionalIncomingINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_endpoint_accept_next(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	return res
}

// Add an external (manually-known) socket address that this endpoint is
// reachable on. Useful when running behind a static NAT / load balancer.
func (_self *Endpoint) AddExternalAddr(addr string) error {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_endpoint_add_external_addr(
			_pointer, FfiConverterStringINSTANCE.Lower(addr)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// The [`EndpointAddr`] for this endpoint (id + currently known addresses).
func (_self *Endpoint) Addr() *EndpointAddr {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterEndpointAddrINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpoint_addr(
			_pointer, _uniffiStatus)
	}))
}

// The local socket addresses this endpoint is bound to.
func (_self *Endpoint) BoundSockets() []string {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpoint_bound_sockets(
				_pointer, _uniffiStatus),
		}
	}))
}

// Shut down the endpoint (and, if present, the protocol router).
func (_self *Endpoint) Close() error {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_endpoint_close(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// Connect to a remote endpoint via the given ALPN.
func (_self *Endpoint) Connect(addr *EndpointAddr, alpn []byte) (*Connection, error) {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *Connection {
			return FfiConverterConnectionINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_endpoint_connect(
			_pointer, FfiConverterEndpointAddrINSTANCE.Lower(addr), FfiConverterBytesINSTANCE.Lower(alpn)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Begin a connection attempt to `addr` for `alpn`, returning the
// in-progress [`Connecting`] state.
//
// Unlike [`Self::connect`], which awaits the handshake before returning,
// this exposes the pre-handshake handle so the caller can inspect ALPN or
// drop the attempt explicitly.
func (_self *Endpoint) ConnectPending(addr *EndpointAddr, alpn []byte) (*Connecting, error) {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *Connecting {
			return FfiConverterConnectingINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_endpoint_connect_pending(
			_pointer, FfiConverterEndpointAddrINSTANCE.Lower(addr), FfiConverterBytesINSTANCE.Lower(alpn)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// The [`EndpointId`] of this endpoint.
func (_self *Endpoint) Id() *EndpointId {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterEndpointIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpoint_id(
			_pointer, _uniffiStatus)
	}))
}

// Insert (or replace) a relay configuration at runtime.
func (_self *Endpoint) InsertRelay(config RelayConfig) error {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_endpoint_insert_relay(
			_pointer, FfiConverterRelayConfigINSTANCE.Lower(config)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// Returns true if the endpoint has been closed.
func (_self *Endpoint) IsClosed() bool {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_endpoint_is_closed(
			_pointer, _uniffiStatus)
	}))
}

// Resolves once the endpoint has a usable home relay.
func (_self *Endpoint) Online() {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_endpoint_online(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

}

// Look up cached information about a remote endpoint, if any.
func (_self *Endpoint) RemoteAddr(id *EndpointId) **EndpointAddr {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	res, _ := uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) **EndpointAddr {
			return FfiConverterOptionalEndpointAddrINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_endpoint_remote_addr(
			_pointer, FfiConverterEndpointIdINSTANCE.Lower(id)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	return res
}

// Remove a previously-added external address. Returns true if an entry was
// removed.
func (_self *Endpoint) RemoveExternalAddr(addr string) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.int8_t {
			res := C.ffi_iroh_ffi_rust_future_complete_i8(handle, status)
			return res
		},
		// liftFn
		func(ffi C.int8_t) bool {
			return FfiConverterBoolINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_endpoint_remove_external_addr(
			_pointer, FfiConverterStringINSTANCE.Lower(addr)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_i8(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_i8(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Remove a relay configuration at runtime. Returns true if a relay was
// removed.
func (_self *Endpoint) RemoveRelay(url string) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.int8_t {
			res := C.ffi_iroh_ffi_rust_future_complete_i8(handle, status)
			return res
		},
		// liftFn
		func(ffi C.int8_t) bool {
			return FfiConverterBoolINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_endpoint_remove_relay(
			_pointer, FfiConverterStringINSTANCE.Lower(url)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_i8(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_i8(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// The [`SecretKey`] backing this endpoint's identity.
func (_self *Endpoint) SecretKey() *SecretKey {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSecretKeyINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpoint_secret_key(
			_pointer, _uniffiStatus)
	}))
}

// Replace the set of ALPNs advertised by this endpoint.
func (_self *Endpoint) SetAlpns(alpns [][]byte) {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpoint_set_alpns(
			_pointer, FfiConverterSequenceBytesINSTANCE.Lower(alpns), _uniffiStatus)
		return false
	})
}

// Get current statistics for this endpoint.
//
// Keys are `"<group>:<metric>"`. Counter / gauge values are saturating-cast to `u32`.
func (_self *Endpoint) Stats() map[string]CounterStats {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterMapStringCounterStatsINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpoint_stats(
				_pointer, _uniffiStatus),
		}
	}))
}

// Register a callback that fires whenever the endpoint's [`EndpointAddr`]
// changes (relay home rotates, IP discovered, etc.). The returned
// [`WatchHandle`] cancels the watcher when dropped or when its `stop()`
// method is called.
func (_self *Endpoint) WatchAddr(callback AddrChangeCallback) *WatchHandle {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterWatchHandleINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpoint_watch_addr(
			_pointer, FfiConverterAddrChangeCallbackINSTANCE.Lower(callback), _uniffiStatus)
	}))
}

// Register a callback that fires whenever the list of relays this endpoint
// is currently connected to changes.
func (_self *Endpoint) WatchHomeRelay(callback HomeRelayCallback) *WatchHandle {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterWatchHandleINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpoint_watch_home_relay(
			_pointer, FfiConverterHomeRelayCallbackINSTANCE.Lower(callback), _uniffiStatus)
	}))
}

// Register a callback that fires every time the underlying network stack
// reports a change (interface up/down, NAT change, roaming, etc.).
func (_self *Endpoint) WatchNetworkChange(callback NetworkChangeCallback) *WatchHandle {
	_pointer := _self.ffiObject.incrementPointer("*Endpoint")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterWatchHandleINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpoint_watch_network_change(
			_pointer, FfiConverterNetworkChangeCallbackINSTANCE.Lower(callback), _uniffiStatus)
	}))
}
func (object *Endpoint) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterEndpoint struct{}

var FfiConverterEndpointINSTANCE = FfiConverterEndpoint{}

func (c FfiConverterEndpoint) Lift(handle C.uint64_t) *Endpoint {
	result := &Endpoint{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_endpoint(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_endpoint(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Endpoint).Destroy)
	return result
}

func (c FfiConverterEndpoint) Read(reader io.Reader) *Endpoint {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterEndpoint) Lower(value *Endpoint) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Endpoint")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterEndpoint) Write(writer io.Writer, value *Endpoint) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalEndpoint(handle uint64) *Endpoint {
	return FfiConverterEndpointINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalEndpoint(value *Endpoint) uint64 {
	return uint64(FfiConverterEndpointINSTANCE.Lower(value))
}

type FfiDestroyerEndpoint struct{}

func (_ FfiDestroyerEndpoint) Destroy(value *Endpoint) {
	value.Destroy()
}

// An endpoint's id together with the network-level addresses where it can be reached.
//
// Mirrors `iroh::EndpointAddr` — exposes a flat view over the underlying set of
// `TransportAddr`s (one relay URL plus a list of IP/port pairs).
type EndpointAddrInterface interface {
	// The direct (IP/port) addresses of this peer.
	DirectAddresses() []string
	// The endpoint id.
	Id() *EndpointId
	// The home relay URL for this peer, if known.
	RelayUrl() *string
}

// An endpoint's id together with the network-level addresses where it can be reached.
//
// Mirrors `iroh::EndpointAddr` — exposes a flat view over the underlying set of
// `TransportAddr`s (one relay URL plus a list of IP/port pairs).
type EndpointAddr struct {
	ffiObject FfiObject
}

// Create a new [`EndpointAddr`].
func NewEndpointAddr(id *EndpointId, relayUrl *string, addresses []string) *EndpointAddr {
	return FfiConverterEndpointAddrINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_endpointaddr_new(FfiConverterEndpointIdINSTANCE.Lower(id), FfiConverterOptionalStringINSTANCE.Lower(relayUrl), FfiConverterSequenceStringINSTANCE.Lower(addresses), _uniffiStatus)
	}))
}

// The direct (IP/port) addresses of this peer.
func (_self *EndpointAddr) DirectAddresses() []string {
	_pointer := _self.ffiObject.incrementPointer("*EndpointAddr")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpointaddr_direct_addresses(
				_pointer, _uniffiStatus),
		}
	}))
}

// The endpoint id.
func (_self *EndpointAddr) Id() *EndpointId {
	_pointer := _self.ffiObject.incrementPointer("*EndpointAddr")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterEndpointIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpointaddr_id(
			_pointer, _uniffiStatus)
	}))
}

// The home relay URL for this peer, if known.
func (_self *EndpointAddr) RelayUrl() *string {
	_pointer := _self.ffiObject.incrementPointer("*EndpointAddr")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterOptionalStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpointaddr_relay_url(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *EndpointAddr) String() string {
	_pointer := _self.ffiObject.incrementPointer("*EndpointAddr")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpointaddr_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *EndpointAddr) Eq(other *EndpointAddr) bool {
	_pointer := _self.ffiObject.incrementPointer("*EndpointAddr")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_endpointaddr_uniffi_trait_eq_eq(
			_pointer, FfiConverterEndpointAddrINSTANCE.Lower(other), _uniffiStatus)
	}))
}

func (_self *EndpointAddr) Ne(other *EndpointAddr) bool {
	_pointer := _self.ffiObject.incrementPointer("*EndpointAddr")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_endpointaddr_uniffi_trait_eq_ne(
			_pointer, FfiConverterEndpointAddrINSTANCE.Lower(other), _uniffiStatus)
	}))
}

func (_self *EndpointAddr) Hash() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*EndpointAddr")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpointaddr_uniffi_trait_hash(
			_pointer, _uniffiStatus)
	}))
}

func (object *EndpointAddr) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterEndpointAddr struct{}

var FfiConverterEndpointAddrINSTANCE = FfiConverterEndpointAddr{}

func (c FfiConverterEndpointAddr) Lift(handle C.uint64_t) *EndpointAddr {
	result := &EndpointAddr{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_endpointaddr(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_endpointaddr(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*EndpointAddr).Destroy)
	return result
}

func (c FfiConverterEndpointAddr) Read(reader io.Reader) *EndpointAddr {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterEndpointAddr) Lower(value *EndpointAddr) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*EndpointAddr")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterEndpointAddr) Write(writer io.Writer, value *EndpointAddr) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalEndpointAddr(handle uint64) *EndpointAddr {
	return FfiConverterEndpointAddrINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalEndpointAddr(value *EndpointAddr) uint64 {
	return uint64(FfiConverterEndpointAddrINSTANCE.Lower(value))
}

type FfiDestroyerEndpointAddr struct{}

func (_ FfiDestroyerEndpointAddr) Destroy(value *EndpointAddr) {
	value.Destroy()
}

// A mutable handle to an endpoint builder, handed to [`Preset::apply`].
//
// Mirrors the chainable surface of `iroh::endpoint::Builder` that a preset
// cares about. The three `apply_*` methods replay the corresponding upstream
// `iroh::endpoint::presets` impl (which, importantly, install the crypto
// provider) — a custom preset will almost always call one of them as a
// baseline before layering its own configuration.
type EndpointBuilderInterface interface {
	// Set the advertised ALPNs.
	Alpns(alpns [][]byte)
	// Replay the minimal preset (crypto provider only, no external deps).
	ApplyMinimal()
	// Replay the n0 production preset (relays + discovery + crypto provider).
	ApplyN0()
	// Replay the n0 preset with relays disabled.
	ApplyN0DisableRelay()
	// Consume the builder and bind a new [`Endpoint`].
	//
	// The returned `Endpoint` has no protocol handlers — use
	// [`Endpoint::bind`] with [`EndpointOptions::protocols`] to attach them.
	// The builder is single-use; a second `bind` returns
	// `EndpointBuilder already consumed`.
	Bind() (*Endpoint, error)
	// Set the address the endpoint binds to (`host:port`).
	BindAddr(addr string) error
	// Set the relay mode.
	RelayMode(mode *RelayMode)
	// Set the endpoint secret key (32 bytes).
	SecretKey(bytes []byte) error
}

// A mutable handle to an endpoint builder, handed to [`Preset::apply`].
//
// Mirrors the chainable surface of `iroh::endpoint::Builder` that a preset
// cares about. The three `apply_*` methods replay the corresponding upstream
// `iroh::endpoint::presets` impl (which, importantly, install the crypto
// provider) — a custom preset will almost always call one of them as a
// baseline before layering its own configuration.
type EndpointBuilder struct {
	ffiObject FfiObject
}

// Create a fresh empty endpoint builder. Apply a preset (`apply_n0`,
// `apply_minimal`, `apply_n0_disable_relay`) before [`bind`](Self::bind);
// the preset installs the crypto provider, without one `bind` will error.
func NewEndpointBuilder() *EndpointBuilder {
	return FfiConverterEndpointBuilderINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_endpointbuilder_new(_uniffiStatus)
	}))
}

// Set the advertised ALPNs.
func (_self *EndpointBuilder) Alpns(alpns [][]byte) {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_alpns(
			_pointer, FfiConverterSequenceBytesINSTANCE.Lower(alpns), _uniffiStatus)
		return false
	})
}

// Replay the minimal preset (crypto provider only, no external deps).
func (_self *EndpointBuilder) ApplyMinimal() {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_apply_minimal(
			_pointer, _uniffiStatus)
		return false
	})
}

// Replay the n0 production preset (relays + discovery + crypto provider).
func (_self *EndpointBuilder) ApplyN0() {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_apply_n0(
			_pointer, _uniffiStatus)
		return false
	})
}

// Replay the n0 preset with relays disabled.
func (_self *EndpointBuilder) ApplyN0DisableRelay() {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_apply_n0_disable_relay(
			_pointer, _uniffiStatus)
		return false
	})
}

// Consume the builder and bind a new [`Endpoint`].
//
// The returned `Endpoint` has no protocol handlers — use
// [`Endpoint::bind`] with [`EndpointOptions::protocols`] to attach them.
// The builder is single-use; a second `bind` returns
// `EndpointBuilder already consumed`.
func (_self *EndpointBuilder) Bind() (*Endpoint, error) {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *Endpoint {
			return FfiConverterEndpointINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_bind(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Set the address the endpoint binds to (`host:port`).
func (_self *EndpointBuilder) BindAddr(addr string) error {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_bind_addr(
			_pointer, FfiConverterStringINSTANCE.Lower(addr), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// Set the relay mode.
func (_self *EndpointBuilder) RelayMode(mode *RelayMode) {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_relay_mode(
			_pointer, FfiConverterRelayModeINSTANCE.Lower(mode), _uniffiStatus)
		return false
	})
}

// Set the endpoint secret key (32 bytes).
func (_self *EndpointBuilder) SecretKey(bytes []byte) error {
	_pointer := _self.ffiObject.incrementPointer("*EndpointBuilder")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointbuilder_secret_key(
			_pointer, FfiConverterBytesINSTANCE.Lower(bytes), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}
func (object *EndpointBuilder) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterEndpointBuilder struct{}

var FfiConverterEndpointBuilderINSTANCE = FfiConverterEndpointBuilder{}

func (c FfiConverterEndpointBuilder) Lift(handle C.uint64_t) *EndpointBuilder {
	result := &EndpointBuilder{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_endpointbuilder(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_endpointbuilder(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*EndpointBuilder).Destroy)
	return result
}

func (c FfiConverterEndpointBuilder) Read(reader io.Reader) *EndpointBuilder {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterEndpointBuilder) Lower(value *EndpointBuilder) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*EndpointBuilder")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterEndpointBuilder) Write(writer io.Writer, value *EndpointBuilder) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalEndpointBuilder(handle uint64) *EndpointBuilder {
	return FfiConverterEndpointBuilderINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalEndpointBuilder(value *EndpointBuilder) uint64 {
	return uint64(FfiConverterEndpointBuilderINSTANCE.Lower(value))
}

type FfiDestroyerEndpointBuilder struct{}

func (_ FfiDestroyerEndpointBuilder) Destroy(value *EndpointBuilder) {
	value.Destroy()
}

// An endpoint's identifier, a 32-byte ed25519 public key.
//
// In iroh 1.0 this is an alias for the underlying `PublicKey` cryptographic type
// and uniquely identifies an [`Endpoint`](crate::Endpoint).
type EndpointIdInterface interface {
	// Short, base32 prefix of the [`EndpointId`].
	FmtShort() string
	// Get the underlying 32 bytes.
	ToBytes() []byte
	// Verify a signature on `message` against this endpoint's key.
	Verify(message []byte, signature *Signature) error
}

// An endpoint's identifier, a 32-byte ed25519 public key.
//
// In iroh 1.0 this is an alias for the underlying `PublicKey` cryptographic type
// and uniquely identifies an [`Endpoint`](crate::Endpoint).
type EndpointId struct {
	ffiObject FfiObject
}

// Construct an [`EndpointId`] from raw bytes.
func EndpointIdFromBytes(bytes []byte) (*EndpointId, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_endpointid_from_bytes(FfiConverterBytesINSTANCE.Lower(bytes), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *EndpointId
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterEndpointIdINSTANCE.Lift(_uniffiRV), nil
	}
}

// Parse an [`EndpointId`] from its base32 representation.
func EndpointIdFromString(s string) (*EndpointId, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_endpointid_from_string(FfiConverterStringINSTANCE.Lower(s), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *EndpointId
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterEndpointIdINSTANCE.Lift(_uniffiRV), nil
	}
}

// Short, base32 prefix of the [`EndpointId`].
func (_self *EndpointId) FmtShort() string {
	_pointer := _self.ffiObject.incrementPointer("*EndpointId")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpointid_fmt_short(
				_pointer, _uniffiStatus),
		}
	}))
}

// Get the underlying 32 bytes.
func (_self *EndpointId) ToBytes() []byte {
	_pointer := _self.ffiObject.incrementPointer("*EndpointId")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBytesINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpointid_to_bytes(
				_pointer, _uniffiStatus),
		}
	}))
}

// Verify a signature on `message` against this endpoint's key.
func (_self *EndpointId) Verify(message []byte, signature *Signature) error {
	_pointer := _self.ffiObject.incrementPointer("*EndpointId")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_endpointid_verify(
			_pointer, FfiConverterBytesINSTANCE.Lower(message), FfiConverterSignatureINSTANCE.Lower(signature), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

func (_self *EndpointId) String() string {
	_pointer := _self.ffiObject.incrementPointer("*EndpointId")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpointid_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *EndpointId) Eq(other *EndpointId) bool {
	_pointer := _self.ffiObject.incrementPointer("*EndpointId")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_endpointid_uniffi_trait_eq_eq(
			_pointer, FfiConverterEndpointIdINSTANCE.Lower(other), _uniffiStatus)
	}))
}

func (_self *EndpointId) Ne(other *EndpointId) bool {
	_pointer := _self.ffiObject.incrementPointer("*EndpointId")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_endpointid_uniffi_trait_eq_ne(
			_pointer, FfiConverterEndpointIdINSTANCE.Lower(other), _uniffiStatus)
	}))
}

func (_self *EndpointId) Hash() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*EndpointId")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpointid_uniffi_trait_hash(
			_pointer, _uniffiStatus)
	}))
}

func (object *EndpointId) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterEndpointId struct{}

var FfiConverterEndpointIdINSTANCE = FfiConverterEndpointId{}

func (c FfiConverterEndpointId) Lift(handle C.uint64_t) *EndpointId {
	result := &EndpointId{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_endpointid(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_endpointid(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*EndpointId).Destroy)
	return result
}

func (c FfiConverterEndpointId) Read(reader io.Reader) *EndpointId {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterEndpointId) Lower(value *EndpointId) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*EndpointId")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterEndpointId) Write(writer io.Writer, value *EndpointId) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalEndpointId(handle uint64) *EndpointId {
	return FfiConverterEndpointIdINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalEndpointId(value *EndpointId) uint64 {
	return uint64(FfiConverterEndpointIdINSTANCE.Lower(value))
}

type FfiDestroyerEndpointId struct{}

func (_ FfiDestroyerEndpointId) Destroy(value *EndpointId) {
	value.Destroy()
}

// A token containing information for establishing a connection to an endpoint.
//
// This allows establishing a connection to the endpoint in most circumstances where
// it is possible to do so. It is a single item that can be easily serialized and
// deserialized to/from a base32 string.
type EndpointTicketInterface interface {
	// The [`EndpointAddr`] embedded in this ticket.
	EndpointAddr() *EndpointAddr
}

// A token containing information for establishing a connection to an endpoint.
//
// This allows establishing a connection to the endpoint in most circumstances where
// it is possible to do so. It is a single item that can be easily serialized and
// deserialized to/from a base32 string.
type EndpointTicket struct {
	ffiObject FfiObject
}

// Wrap the given [`EndpointAddr`] as an [`EndpointTicket`].
//
// The returned ticket can be serialized via [`Self::to_string`] and parsed back
// using [`Self::from_string`].
func EndpointTicketFromAddr(addr *EndpointAddr) (*EndpointTicket, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_endpointticket_from_addr(FfiConverterEndpointAddrINSTANCE.Lower(addr), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *EndpointTicket
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterEndpointTicketINSTANCE.Lift(_uniffiRV), nil
	}
}

// Parse an [`EndpointTicket`] from its string presentation.
func EndpointTicketFromString(str string) (*EndpointTicket, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_endpointticket_from_string(FfiConverterStringINSTANCE.Lower(str), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *EndpointTicket
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterEndpointTicketINSTANCE.Lift(_uniffiRV), nil
	}
}

// The [`EndpointAddr`] embedded in this ticket.
func (_self *EndpointTicket) EndpointAddr() *EndpointAddr {
	_pointer := _self.ffiObject.incrementPointer("*EndpointTicket")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterEndpointAddrINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_endpointticket_endpoint_addr(
			_pointer, _uniffiStatus)
	}))
}

func (_self *EndpointTicket) String() string {
	_pointer := _self.ffiObject.incrementPointer("*EndpointTicket")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_endpointticket_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (object *EndpointTicket) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterEndpointTicket struct{}

var FfiConverterEndpointTicketINSTANCE = FfiConverterEndpointTicket{}

func (c FfiConverterEndpointTicket) Lift(handle C.uint64_t) *EndpointTicket {
	result := &EndpointTicket{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_endpointticket(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_endpointticket(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*EndpointTicket).Destroy)
	return result
}

func (c FfiConverterEndpointTicket) Read(reader io.Reader) *EndpointTicket {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterEndpointTicket) Lower(value *EndpointTicket) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*EndpointTicket")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterEndpointTicket) Write(writer io.Writer, value *EndpointTicket) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalEndpointTicket(handle uint64) *EndpointTicket {
	return FfiConverterEndpointTicketINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalEndpointTicket(value *EndpointTicket) uint64 {
	return uint64(FfiConverterEndpointTicketINSTANCE.Lower(value))
}

type FfiDestroyerEndpointTicket struct{}

func (_ FfiDestroyerEndpointTicket) Destroy(value *EndpointTicket) {
	value.Destroy()
}

// Callback invoked whenever the home-relay connection status list changes.
type HomeRelayCallback interface {
	OnChange(relayUrls []string) error
}

// Callback invoked whenever the home-relay connection status list changes.
type HomeRelayCallbackImpl struct {
	ffiObject FfiObject
}

func (_self *HomeRelayCallbackImpl) OnChange(relayUrls []string) error {
	_pointer := _self.ffiObject.incrementPointer("HomeRelayCallback")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*CallbackError](
		FfiConverterCallbackErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_homerelaycallback_on_change(
			_pointer, FfiConverterSequenceStringINSTANCE.Lower(relayUrls)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *HomeRelayCallbackImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterHomeRelayCallback struct {
	handleMap *concurrentHandleMap[HomeRelayCallback]
}

var FfiConverterHomeRelayCallbackINSTANCE = FfiConverterHomeRelayCallback{
	handleMap: newConcurrentHandleMap[HomeRelayCallback](),
}

func (c FfiConverterHomeRelayCallback) Lift(handle C.uint64_t) HomeRelayCallback {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &HomeRelayCallbackImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_homerelaycallback(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_homerelaycallback(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*HomeRelayCallbackImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterHomeRelayCallback) Read(reader io.Reader) HomeRelayCallback {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterHomeRelayCallback) Lower(value HomeRelayCallback) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*HomeRelayCallbackImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("HomeRelayCallback")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterHomeRelayCallback) Write(writer io.Writer, value HomeRelayCallback) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalHomeRelayCallback(handle uint64) HomeRelayCallback {
	return FfiConverterHomeRelayCallbackINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalHomeRelayCallback(value HomeRelayCallback) uint64 {
	return uint64(FfiConverterHomeRelayCallbackINSTANCE.Lower(value))
}

type FfiDestroyerHomeRelayCallback struct{}

func (_ FfiDestroyerHomeRelayCallback) Destroy(value HomeRelayCallback) {
	if val, ok := value.(*HomeRelayCallbackImpl); ok {
		val.Destroy()
	}
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackMethod0
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackMethod0(uniffiHandle C.uint64_t, relayUrls C.RustBuffer, uniffiFutureCallback C.UniffiForeignFutureCompleteVoid, uniffiCallbackData C.uint64_t, uniffiOutDroppedCallback *C.UniffiForeignFutureDroppedCallbackStruct) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterHomeRelayCallbackINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	result := make(chan C.UniffiForeignFutureResultVoid, 1)
	cancel := make(chan struct{}, 1)
	guardHandle := cgo.NewHandle(cancel)
	*uniffiOutDroppedCallback = C.UniffiForeignFutureDroppedCallbackStruct{
		handle: C.uint64_t(guardHandle),
		free:   C.UniffiForeignFutureDroppedCallback(C.iroh_uniffiFreeGorutine),
	}

	// Wait for compleation or cancel
	go func() {
		select {
		case <-cancel:
		case res := <-result:
			C.call_UniffiForeignFutureCompleteVoid(uniffiFutureCallback, uniffiCallbackData, res)
		}
	}()

	// Eval callback asynchroniously
	go func() {
		asyncResult := &C.UniffiForeignFutureResultVoid{}
		callStatus := &asyncResult.callStatus
		defer func() {
			result <- *asyncResult
		}()

		err :=
			uniffiObj.OnChange(
				FfiConverterSequenceStringINSTANCE.Lift(GoRustBuffer{
					inner: relayUrls,
				}),
			)

		if err != nil {
			var actualError *CallbackError
			if errors.As(err, &actualError) {
				*callStatus = C.RustCallStatus{
					code:     C.int8_t(uniffiCallbackResultError),
					errorBuf: FfiConverterCallbackErrorINSTANCE.Lower(actualError),
				}
			} else {
				*callStatus = C.RustCallStatus{
					code: C.int8_t(uniffiCallbackUnexpectedResultError),
				}
			}
			return
		}

	}()
}

var UniffiVTableCallbackInterfaceHomeRelayCallbackINSTANCE = C.UniffiVTableCallbackInterfaceHomeRelayCallback{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackClone),
	onChange:    (C.UniffiCallbackInterfaceHomeRelayCallbackMethod0)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackMethod0),
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackFree
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackFree(handle C.uint64_t) {
	FfiConverterHomeRelayCallbackINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackClone
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceHomeRelayCallbackClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterHomeRelayCallbackINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterHomeRelayCallbackINSTANCE.handleMap.insert(val))
}

func (c FfiConverterHomeRelayCallback) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_homerelaycallback(&UniffiVTableCallbackInterfaceHomeRelayCallbackINSTANCE)
}

// An incoming connection that has not yet begun its server-side handshake.
//
// Consume via [`Self::accept`] / [`Self::refuse`] / [`Self::retry`] / [`Self::ignore`].
// Each `Incoming` can only be consumed once.
type IncomingInterface interface {
	// Begin the server-side handshake, producing an [`Accepting`].
	Accept() (*Accepting, error)
	// Drop this incoming connection without sending any reply.
	Ignore() error
	// The local address that received this incoming connection.
	LocalAddr() (IncomingLocalAddr, error)
	// Reject this incoming connection attempt.
	Refuse() error
	// The remote address that originated this incoming connection.
	RemoteAddr() (IncomingAddr, error)
	// True if the remote address has been validated by the QUIC retry mechanism.
	RemoteAddrValidated() (bool, error)
	// Respond with a retry packet, requiring the client to retry with address
	// validation.
	Retry() error
}

// An incoming connection that has not yet begun its server-side handshake.
//
// Consume via [`Self::accept`] / [`Self::refuse`] / [`Self::retry`] / [`Self::ignore`].
// Each `Incoming` can only be consumed once.
type Incoming struct {
	ffiObject FfiObject
}

// Begin the server-side handshake, producing an [`Accepting`].
func (_self *Incoming) Accept() (*Accepting, error) {
	_pointer := _self.ffiObject.incrementPointer("*Incoming")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *Accepting {
			return FfiConverterAcceptingINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_incoming_accept(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Drop this incoming connection without sending any reply.
func (_self *Incoming) Ignore() error {
	_pointer := _self.ffiObject.incrementPointer("*Incoming")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_incoming_ignore(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// The local address that received this incoming connection.
func (_self *Incoming) LocalAddr() (IncomingLocalAddr, error) {
	_pointer := _self.ffiObject.incrementPointer("*Incoming")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) IncomingLocalAddr {
			return FfiConverterIncomingLocalAddrINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_incoming_local_addr(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Reject this incoming connection attempt.
func (_self *Incoming) Refuse() error {
	_pointer := _self.ffiObject.incrementPointer("*Incoming")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_incoming_refuse(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// The remote address that originated this incoming connection.
func (_self *Incoming) RemoteAddr() (IncomingAddr, error) {
	_pointer := _self.ffiObject.incrementPointer("*Incoming")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) IncomingAddr {
			return FfiConverterIncomingAddrINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_incoming_remote_addr(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// True if the remote address has been validated by the QUIC retry mechanism.
func (_self *Incoming) RemoteAddrValidated() (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*Incoming")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.int8_t {
			res := C.ffi_iroh_ffi_rust_future_complete_i8(handle, status)
			return res
		},
		// liftFn
		func(ffi C.int8_t) bool {
			return FfiConverterBoolINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_incoming_remote_addr_validated(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_i8(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_i8(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Respond with a retry packet, requiring the client to retry with address
// validation.
func (_self *Incoming) Retry() error {
	_pointer := _self.ffiObject.incrementPointer("*Incoming")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_incoming_retry(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *Incoming) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterIncoming struct{}

var FfiConverterIncomingINSTANCE = FfiConverterIncoming{}

func (c FfiConverterIncoming) Lift(handle C.uint64_t) *Incoming {
	result := &Incoming{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_incoming(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_incoming(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Incoming).Destroy)
	return result
}

func (c FfiConverterIncoming) Read(reader io.Reader) *Incoming {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterIncoming) Lower(value *Incoming) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Incoming")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterIncoming) Write(writer io.Writer, value *Incoming) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalIncoming(handle uint64) *Incoming {
	return FfiConverterIncomingINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalIncoming(value *Incoming) uint64 {
	return uint64(FfiConverterIncomingINSTANCE.Lower(value))
}

type FfiDestroyerIncoming struct{}

func (_ FfiDestroyerIncoming) Destroy(value *Incoming) {
	value.Destroy()
}

// An Error.
type IrohErrorInterface interface {
	// Detailed debug representation of the original Rust error.
	DebugMessage() string
	// Convenience helper for bindings that do not expose enum comparison
	// ergonomically.
	IsKind(kind IrohErrorKind) bool
	// Stable high-level error category.
	Kind() IrohErrorKind
	// Human-readable error message.
	Message() string
}

// An Error.
type IrohError struct {
	ffiObject FfiObject
}

// Detailed debug representation of the original Rust error.
func (_self *IrohError) DebugMessage() string {
	_pointer := _self.ffiObject.incrementPointer("*IrohError")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_iroherror_debug_message(
				_pointer, _uniffiStatus),
		}
	}))
}

// Convenience helper for bindings that do not expose enum comparison
// ergonomically.
func (_self *IrohError) IsKind(kind IrohErrorKind) bool {
	_pointer := _self.ffiObject.incrementPointer("*IrohError")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_iroherror_is_kind(
			_pointer, FfiConverterIrohErrorKindINSTANCE.Lower(kind), _uniffiStatus)
	}))
}

// Stable high-level error category.
func (_self *IrohError) Kind() IrohErrorKind {
	_pointer := _self.ffiObject.incrementPointer("*IrohError")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterIrohErrorKindINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_iroherror_kind(
				_pointer, _uniffiStatus),
		}
	}))
}

// Human-readable error message.
func (_self *IrohError) Message() string {
	_pointer := _self.ffiObject.incrementPointer("*IrohError")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_iroherror_message(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *IrohError) DebugString() string {
	_pointer := _self.ffiObject.incrementPointer("*IrohError")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_iroherror_uniffi_trait_debug(
				_pointer, _uniffiStatus),
		}
	}))
}

func (object *IrohError) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterIrohError struct{}

var FfiConverterIrohErrorINSTANCE = FfiConverterIrohError{}

func (_self *IrohError) Error() string {
	return _self.Message()
}

func (_self *IrohError) AsError() error {
	if _self == nil {
		return nil
	} else {
		return _self
	}
}
func (c FfiConverterIrohError) Lift(handle C.uint64_t) *IrohError {
	result := &IrohError{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_iroherror(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_iroherror(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*IrohError).Destroy)
	return result
}

func (c FfiConverterIrohError) Read(reader io.Reader) *IrohError {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterIrohError) Lower(value *IrohError) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*IrohError")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterIrohError) Write(writer io.Writer, value *IrohError) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalIrohError(handle uint64) *IrohError {
	return FfiConverterIrohErrorINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalIrohError(value *IrohError) uint64 {
	return uint64(FfiConverterIrohErrorINSTANCE.Lower(value))
}

type FfiDestroyerIrohError struct{}

func (_ FfiDestroyerIrohError) Destroy(value *IrohError) {
	value.Destroy()
}

// Callback invoked when a network-stack change is detected (interface up/down,
// roaming, etc.).
type NetworkChangeCallback interface {
	OnChange() error
}

// Callback invoked when a network-stack change is detected (interface up/down,
// roaming, etc.).
type NetworkChangeCallbackImpl struct {
	ffiObject FfiObject
}

func (_self *NetworkChangeCallbackImpl) OnChange() error {
	_pointer := _self.ffiObject.incrementPointer("NetworkChangeCallback")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*CallbackError](
		FfiConverterCallbackErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_networkchangecallback_on_change(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *NetworkChangeCallbackImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterNetworkChangeCallback struct {
	handleMap *concurrentHandleMap[NetworkChangeCallback]
}

var FfiConverterNetworkChangeCallbackINSTANCE = FfiConverterNetworkChangeCallback{
	handleMap: newConcurrentHandleMap[NetworkChangeCallback](),
}

func (c FfiConverterNetworkChangeCallback) Lift(handle C.uint64_t) NetworkChangeCallback {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &NetworkChangeCallbackImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_networkchangecallback(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_networkchangecallback(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*NetworkChangeCallbackImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterNetworkChangeCallback) Read(reader io.Reader) NetworkChangeCallback {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterNetworkChangeCallback) Lower(value NetworkChangeCallback) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*NetworkChangeCallbackImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("NetworkChangeCallback")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterNetworkChangeCallback) Write(writer io.Writer, value NetworkChangeCallback) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalNetworkChangeCallback(handle uint64) NetworkChangeCallback {
	return FfiConverterNetworkChangeCallbackINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalNetworkChangeCallback(value NetworkChangeCallback) uint64 {
	return uint64(FfiConverterNetworkChangeCallbackINSTANCE.Lower(value))
}

type FfiDestroyerNetworkChangeCallback struct{}

func (_ FfiDestroyerNetworkChangeCallback) Destroy(value NetworkChangeCallback) {
	if val, ok := value.(*NetworkChangeCallbackImpl); ok {
		val.Destroy()
	}
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackMethod0
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackMethod0(uniffiHandle C.uint64_t, uniffiFutureCallback C.UniffiForeignFutureCompleteVoid, uniffiCallbackData C.uint64_t, uniffiOutDroppedCallback *C.UniffiForeignFutureDroppedCallbackStruct) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterNetworkChangeCallbackINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	result := make(chan C.UniffiForeignFutureResultVoid, 1)
	cancel := make(chan struct{}, 1)
	guardHandle := cgo.NewHandle(cancel)
	*uniffiOutDroppedCallback = C.UniffiForeignFutureDroppedCallbackStruct{
		handle: C.uint64_t(guardHandle),
		free:   C.UniffiForeignFutureDroppedCallback(C.iroh_uniffiFreeGorutine),
	}

	// Wait for compleation or cancel
	go func() {
		select {
		case <-cancel:
		case res := <-result:
			C.call_UniffiForeignFutureCompleteVoid(uniffiFutureCallback, uniffiCallbackData, res)
		}
	}()

	// Eval callback asynchroniously
	go func() {
		asyncResult := &C.UniffiForeignFutureResultVoid{}
		callStatus := &asyncResult.callStatus
		defer func() {
			result <- *asyncResult
		}()

		err :=
			uniffiObj.OnChange()

		if err != nil {
			var actualError *CallbackError
			if errors.As(err, &actualError) {
				*callStatus = C.RustCallStatus{
					code:     C.int8_t(uniffiCallbackResultError),
					errorBuf: FfiConverterCallbackErrorINSTANCE.Lower(actualError),
				}
			} else {
				*callStatus = C.RustCallStatus{
					code: C.int8_t(uniffiCallbackUnexpectedResultError),
				}
			}
			return
		}

	}()
}

var UniffiVTableCallbackInterfaceNetworkChangeCallbackINSTANCE = C.UniffiVTableCallbackInterfaceNetworkChangeCallback{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackClone),
	onChange:    (C.UniffiCallbackInterfaceNetworkChangeCallbackMethod0)(C.iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackMethod0),
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackFree
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackFree(handle C.uint64_t) {
	FfiConverterNetworkChangeCallbackINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackClone
func iroh_ffi_watch_cgo_dispatchCallbackInterfaceNetworkChangeCallbackClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterNetworkChangeCallbackINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterNetworkChangeCallbackINSTANCE.handleMap.insert(val))
}

func (c FfiConverterNetworkChangeCallback) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_networkchangecallback(&UniffiVTableCallbackInterfaceNetworkChangeCallbackINSTANCE)
}

// Callback for `Connection::watch_paths` — fires whenever the open-paths
// snapshot changes (path opens/closes/selection changes).
type PathChangeCallback interface {
	OnChange(paths []PathSnapshot) error
}

// Callback for `Connection::watch_paths` — fires whenever the open-paths
// snapshot changes (path opens/closes/selection changes).
type PathChangeCallbackImpl struct {
	ffiObject FfiObject
}

func (_self *PathChangeCallbackImpl) OnChange(paths []PathSnapshot) error {
	_pointer := _self.ffiObject.incrementPointer("PathChangeCallback")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*CallbackError](
		FfiConverterCallbackErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_pathchangecallback_on_change(
			_pointer, FfiConverterSequencePathSnapshotINSTANCE.Lower(paths)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *PathChangeCallbackImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterPathChangeCallback struct {
	handleMap *concurrentHandleMap[PathChangeCallback]
}

var FfiConverterPathChangeCallbackINSTANCE = FfiConverterPathChangeCallback{
	handleMap: newConcurrentHandleMap[PathChangeCallback](),
}

func (c FfiConverterPathChangeCallback) Lift(handle C.uint64_t) PathChangeCallback {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &PathChangeCallbackImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_pathchangecallback(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_pathchangecallback(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*PathChangeCallbackImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterPathChangeCallback) Read(reader io.Reader) PathChangeCallback {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterPathChangeCallback) Lower(value PathChangeCallback) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*PathChangeCallbackImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("PathChangeCallback")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterPathChangeCallback) Write(writer io.Writer, value PathChangeCallback) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalPathChangeCallback(handle uint64) PathChangeCallback {
	return FfiConverterPathChangeCallbackINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalPathChangeCallback(value PathChangeCallback) uint64 {
	return uint64(FfiConverterPathChangeCallbackINSTANCE.Lower(value))
}

type FfiDestroyerPathChangeCallback struct{}

func (_ FfiDestroyerPathChangeCallback) Destroy(value PathChangeCallback) {
	if val, ok := value.(*PathChangeCallbackImpl); ok {
		val.Destroy()
	}
}

//export iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackMethod0
func iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackMethod0(uniffiHandle C.uint64_t, paths C.RustBuffer, uniffiFutureCallback C.UniffiForeignFutureCompleteVoid, uniffiCallbackData C.uint64_t, uniffiOutDroppedCallback *C.UniffiForeignFutureDroppedCallbackStruct) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterPathChangeCallbackINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	result := make(chan C.UniffiForeignFutureResultVoid, 1)
	cancel := make(chan struct{}, 1)
	guardHandle := cgo.NewHandle(cancel)
	*uniffiOutDroppedCallback = C.UniffiForeignFutureDroppedCallbackStruct{
		handle: C.uint64_t(guardHandle),
		free:   C.UniffiForeignFutureDroppedCallback(C.iroh_uniffiFreeGorutine),
	}

	// Wait for compleation or cancel
	go func() {
		select {
		case <-cancel:
		case res := <-result:
			C.call_UniffiForeignFutureCompleteVoid(uniffiFutureCallback, uniffiCallbackData, res)
		}
	}()

	// Eval callback asynchroniously
	go func() {
		asyncResult := &C.UniffiForeignFutureResultVoid{}
		callStatus := &asyncResult.callStatus
		defer func() {
			result <- *asyncResult
		}()

		err :=
			uniffiObj.OnChange(
				FfiConverterSequencePathSnapshotINSTANCE.Lift(GoRustBuffer{
					inner: paths,
				}),
			)

		if err != nil {
			var actualError *CallbackError
			if errors.As(err, &actualError) {
				*callStatus = C.RustCallStatus{
					code:     C.int8_t(uniffiCallbackResultError),
					errorBuf: FfiConverterCallbackErrorINSTANCE.Lower(actualError),
				}
			} else {
				*callStatus = C.RustCallStatus{
					code: C.int8_t(uniffiCallbackUnexpectedResultError),
				}
			}
			return
		}

	}()
}

var UniffiVTableCallbackInterfacePathChangeCallbackINSTANCE = C.UniffiVTableCallbackInterfacePathChangeCallback{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackClone),
	onChange:    (C.UniffiCallbackInterfacePathChangeCallbackMethod0)(C.iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackMethod0),
}

//export iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackFree
func iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackFree(handle C.uint64_t) {
	FfiConverterPathChangeCallbackINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackClone
func iroh_ffi_path_cgo_dispatchCallbackInterfacePathChangeCallbackClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterPathChangeCallbackINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterPathChangeCallbackINSTANCE.handleMap.insert(val))
}

func (c FfiConverterPathChangeCallback) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_pathchangecallback(&UniffiVTableCallbackInterfacePathChangeCallbackINSTANCE)
}

// Callback for `Connection::watch_path_events` — fires for each individual
// path event.
type PathEventCallback interface {
	OnEvent(event PathEvent) error
}

// Callback for `Connection::watch_path_events` — fires for each individual
// path event.
type PathEventCallbackImpl struct {
	ffiObject FfiObject
}

func (_self *PathEventCallbackImpl) OnEvent(event PathEvent) error {
	_pointer := _self.ffiObject.incrementPointer("PathEventCallback")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*CallbackError](
		FfiConverterCallbackErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_patheventcallback_on_event(
			_pointer, FfiConverterPathEventINSTANCE.Lower(event)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *PathEventCallbackImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterPathEventCallback struct {
	handleMap *concurrentHandleMap[PathEventCallback]
}

var FfiConverterPathEventCallbackINSTANCE = FfiConverterPathEventCallback{
	handleMap: newConcurrentHandleMap[PathEventCallback](),
}

func (c FfiConverterPathEventCallback) Lift(handle C.uint64_t) PathEventCallback {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &PathEventCallbackImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_patheventcallback(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_patheventcallback(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*PathEventCallbackImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterPathEventCallback) Read(reader io.Reader) PathEventCallback {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterPathEventCallback) Lower(value PathEventCallback) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*PathEventCallbackImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("PathEventCallback")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterPathEventCallback) Write(writer io.Writer, value PathEventCallback) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalPathEventCallback(handle uint64) PathEventCallback {
	return FfiConverterPathEventCallbackINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalPathEventCallback(value PathEventCallback) uint64 {
	return uint64(FfiConverterPathEventCallbackINSTANCE.Lower(value))
}

type FfiDestroyerPathEventCallback struct{}

func (_ FfiDestroyerPathEventCallback) Destroy(value PathEventCallback) {
	if val, ok := value.(*PathEventCallbackImpl); ok {
		val.Destroy()
	}
}

//export iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackMethod0
func iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackMethod0(uniffiHandle C.uint64_t, event C.RustBuffer, uniffiFutureCallback C.UniffiForeignFutureCompleteVoid, uniffiCallbackData C.uint64_t, uniffiOutDroppedCallback *C.UniffiForeignFutureDroppedCallbackStruct) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterPathEventCallbackINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	result := make(chan C.UniffiForeignFutureResultVoid, 1)
	cancel := make(chan struct{}, 1)
	guardHandle := cgo.NewHandle(cancel)
	*uniffiOutDroppedCallback = C.UniffiForeignFutureDroppedCallbackStruct{
		handle: C.uint64_t(guardHandle),
		free:   C.UniffiForeignFutureDroppedCallback(C.iroh_uniffiFreeGorutine),
	}

	// Wait for compleation or cancel
	go func() {
		select {
		case <-cancel:
		case res := <-result:
			C.call_UniffiForeignFutureCompleteVoid(uniffiFutureCallback, uniffiCallbackData, res)
		}
	}()

	// Eval callback asynchroniously
	go func() {
		asyncResult := &C.UniffiForeignFutureResultVoid{}
		callStatus := &asyncResult.callStatus
		defer func() {
			result <- *asyncResult
		}()

		err :=
			uniffiObj.OnEvent(
				FfiConverterPathEventINSTANCE.Lift(GoRustBuffer{
					inner: event,
				}),
			)

		if err != nil {
			var actualError *CallbackError
			if errors.As(err, &actualError) {
				*callStatus = C.RustCallStatus{
					code:     C.int8_t(uniffiCallbackResultError),
					errorBuf: FfiConverterCallbackErrorINSTANCE.Lower(actualError),
				}
			} else {
				*callStatus = C.RustCallStatus{
					code: C.int8_t(uniffiCallbackUnexpectedResultError),
				}
			}
			return
		}

	}()
}

var UniffiVTableCallbackInterfacePathEventCallbackINSTANCE = C.UniffiVTableCallbackInterfacePathEventCallback{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackClone),
	onEvent:     (C.UniffiCallbackInterfacePathEventCallbackMethod0)(C.iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackMethod0),
}

//export iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackFree
func iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackFree(handle C.uint64_t) {
	FfiConverterPathEventCallbackINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackClone
func iroh_ffi_path_cgo_dispatchCallbackInterfacePathEventCallbackClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterPathEventCallbackINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterPathEventCallbackINSTANCE.handleMap.insert(val))
}

func (c FfiConverterPathEventCallback) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_patheventcallback(&UniffiVTableCallbackInterfacePathEventCallbackINSTANCE)
}

// Configures a freshly created [`EndpointBuilder`].
//
// This mirrors the upstream `iroh::endpoint::presets::Preset` trait and is
// implementable from the foreign language: implement `apply` to configure the
// builder however you like (typically calling one of the
// [`EndpointBuilder::apply_n0`] / `apply_minimal` / `apply_n0_disable_relay`
// baselines first, since those install the crypto provider). The built-in
// presets are available as [`preset_n0`], [`preset_minimal`], and
// [`preset_n0_disable_relay`].
type Preset interface {
	Apply(builder *EndpointBuilder)
}

// Configures a freshly created [`EndpointBuilder`].
//
// This mirrors the upstream `iroh::endpoint::presets::Preset` trait and is
// implementable from the foreign language: implement `apply` to configure the
// builder however you like (typically calling one of the
// [`EndpointBuilder::apply_n0`] / `apply_minimal` / `apply_n0_disable_relay`
// baselines first, since those install the crypto provider). The built-in
// presets are available as [`preset_n0`], [`preset_minimal`], and
// [`preset_n0_disable_relay`].
type PresetImpl struct {
	ffiObject FfiObject
}

func (_self *PresetImpl) Apply(builder *EndpointBuilder) {
	_pointer := _self.ffiObject.incrementPointer("Preset")
	defer _self.ffiObject.decrementPointer()
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_preset_apply(
			_pointer, FfiConverterEndpointBuilderINSTANCE.Lower(builder), _uniffiStatus)
		return false
	})
}
func (object *PresetImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterPreset struct {
	handleMap *concurrentHandleMap[Preset]
}

var FfiConverterPresetINSTANCE = FfiConverterPreset{
	handleMap: newConcurrentHandleMap[Preset](),
}

func (c FfiConverterPreset) Lift(handle C.uint64_t) Preset {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &PresetImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_preset(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_preset(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*PresetImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterPreset) Read(reader io.Reader) Preset {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterPreset) Lower(value Preset) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*PresetImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("Preset")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterPreset) Write(writer io.Writer, value Preset) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalPreset(handle uint64) Preset {
	return FfiConverterPresetINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalPreset(value Preset) uint64 {
	return uint64(FfiConverterPresetINSTANCE.Lower(value))
}

type FfiDestroyerPreset struct{}

func (_ FfiDestroyerPreset) Destroy(value Preset) {
	if val, ok := value.(*PresetImpl); ok {
		val.Destroy()
	}
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetMethod0
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetMethod0(uniffiHandle C.uint64_t, builder C.uint64_t, uniffiOutReturn *C.void, callStatus *C.RustCallStatus) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterPresetINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	uniffiObj.Apply(
		FfiConverterEndpointBuilderINSTANCE.Lift(builder),
	)

}

var UniffiVTableCallbackInterfacePresetINSTANCE = C.UniffiVTableCallbackInterfacePreset{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetClone),
	apply:       (C.UniffiCallbackInterfacePresetMethod0)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetMethod0),
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetFree
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetFree(handle C.uint64_t) {
	FfiConverterPresetINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetClone
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfacePresetClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterPresetINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterPresetINSTANCE.handleMap.insert(val))
}

func (c FfiConverterPreset) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_preset(&UniffiVTableCallbackInterfacePresetINSTANCE)
}

type ProtocolCreator interface {
	Create(endpoint *Endpoint) ProtocolHandler
}
type ProtocolCreatorImpl struct {
	ffiObject FfiObject
}

func (_self *ProtocolCreatorImpl) Create(endpoint *Endpoint) ProtocolHandler {
	_pointer := _self.ffiObject.incrementPointer("ProtocolCreator")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterProtocolHandlerINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_protocolcreator_create(
			_pointer, FfiConverterEndpointINSTANCE.Lower(endpoint), _uniffiStatus)
	}))
}
func (object *ProtocolCreatorImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterProtocolCreator struct {
	handleMap *concurrentHandleMap[ProtocolCreator]
}

var FfiConverterProtocolCreatorINSTANCE = FfiConverterProtocolCreator{
	handleMap: newConcurrentHandleMap[ProtocolCreator](),
}

func (c FfiConverterProtocolCreator) Lift(handle C.uint64_t) ProtocolCreator {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &ProtocolCreatorImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_protocolcreator(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_protocolcreator(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*ProtocolCreatorImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterProtocolCreator) Read(reader io.Reader) ProtocolCreator {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterProtocolCreator) Lower(value ProtocolCreator) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*ProtocolCreatorImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("ProtocolCreator")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterProtocolCreator) Write(writer io.Writer, value ProtocolCreator) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalProtocolCreator(handle uint64) ProtocolCreator {
	return FfiConverterProtocolCreatorINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalProtocolCreator(value ProtocolCreator) uint64 {
	return uint64(FfiConverterProtocolCreatorINSTANCE.Lower(value))
}

type FfiDestroyerProtocolCreator struct{}

func (_ FfiDestroyerProtocolCreator) Destroy(value ProtocolCreator) {
	if val, ok := value.(*ProtocolCreatorImpl); ok {
		val.Destroy()
	}
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorMethod0
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorMethod0(uniffiHandle C.uint64_t, endpoint C.uint64_t, uniffiOutReturn *C.uint64_t, callStatus *C.RustCallStatus) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterProtocolCreatorINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	res :=
		uniffiObj.Create(
			FfiConverterEndpointINSTANCE.Lift(endpoint),
		)

	*uniffiOutReturn = FfiConverterProtocolHandlerINSTANCE.Lower(res)
}

var UniffiVTableCallbackInterfaceProtocolCreatorINSTANCE = C.UniffiVTableCallbackInterfaceProtocolCreator{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorClone),
	create:      (C.UniffiCallbackInterfaceProtocolCreatorMethod0)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorMethod0),
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorFree
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorFree(handle C.uint64_t) {
	FfiConverterProtocolCreatorINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorClone
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolCreatorClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterProtocolCreatorINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterProtocolCreatorINSTANCE.handleMap.insert(val))
}

func (c FfiConverterProtocolCreator) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_protocolcreator(&UniffiVTableCallbackInterfaceProtocolCreatorINSTANCE)
}

type ProtocolHandler interface {
	Accept(conn *Connection) error
	Shutdown()
}
type ProtocolHandlerImpl struct {
	ffiObject FfiObject
}

func (_self *ProtocolHandlerImpl) Accept(conn *Connection) error {
	_pointer := _self.ffiObject.incrementPointer("ProtocolHandler")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*CallbackError](
		FfiConverterCallbackErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_protocolhandler_accept(
			_pointer, FfiConverterConnectionINSTANCE.Lower(conn)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

func (_self *ProtocolHandlerImpl) Shutdown() {
	_pointer := _self.ffiObject.incrementPointer("ProtocolHandler")
	defer _self.ffiObject.decrementPointer()
	uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_protocolhandler_shutdown(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

}
func (object *ProtocolHandlerImpl) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterProtocolHandler struct {
	handleMap *concurrentHandleMap[ProtocolHandler]
}

var FfiConverterProtocolHandlerINSTANCE = FfiConverterProtocolHandler{
	handleMap: newConcurrentHandleMap[ProtocolHandler](),
}

func (c FfiConverterProtocolHandler) Lift(handle C.uint64_t) ProtocolHandler {
	if uint64(handle)&1 == 0 {
		// Rust-generated handle (even), construct a new object wrapping the handle
		result := &ProtocolHandlerImpl{
			newFfiObject(
				handle,
				func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
					return C.uniffi_iroh_ffi_fn_clone_protocolhandler(handle, status)
				},
				func(handle C.uint64_t, status *C.RustCallStatus) {
					C.uniffi_iroh_ffi_fn_free_protocolhandler(handle, status)
				},
			),
		}
		runtime.SetFinalizer(result, (*ProtocolHandlerImpl).Destroy)
		return result
	} else {
		// Go-generated handle (odd), retrieve from the handle map
		val, ok := c.handleMap.tryGet(uint64(handle))
		if !ok {
			panic(fmt.Errorf("no callback in handle map: %d", handle))
		}
		c.handleMap.remove(uint64(handle))
		return val
	}
}

func (c FfiConverterProtocolHandler) Read(reader io.Reader) ProtocolHandler {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterProtocolHandler) Lower(value ProtocolHandler) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	if val, ok := value.(*ProtocolHandlerImpl); ok {
		// Rust-backed object, clone the handle
		handle := val.ffiObject.incrementPointer("ProtocolHandler")
		defer val.ffiObject.decrementPointer()
		return handle
	} else {
		// Go-backed object, insert into handle map
		return C.uint64_t(c.handleMap.insert(value))
	}
}

func (c FfiConverterProtocolHandler) Write(writer io.Writer, value ProtocolHandler) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalProtocolHandler(handle uint64) ProtocolHandler {
	return FfiConverterProtocolHandlerINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalProtocolHandler(value ProtocolHandler) uint64 {
	return uint64(FfiConverterProtocolHandlerINSTANCE.Lower(value))
}

type FfiDestroyerProtocolHandler struct{}

func (_ FfiDestroyerProtocolHandler) Destroy(value ProtocolHandler) {
	if val, ok := value.(*ProtocolHandlerImpl); ok {
		val.Destroy()
	}
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerMethod0
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerMethod0(uniffiHandle C.uint64_t, conn C.uint64_t, uniffiFutureCallback C.UniffiForeignFutureCompleteVoid, uniffiCallbackData C.uint64_t, uniffiOutDroppedCallback *C.UniffiForeignFutureDroppedCallbackStruct) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterProtocolHandlerINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	result := make(chan C.UniffiForeignFutureResultVoid, 1)
	cancel := make(chan struct{}, 1)
	guardHandle := cgo.NewHandle(cancel)
	*uniffiOutDroppedCallback = C.UniffiForeignFutureDroppedCallbackStruct{
		handle: C.uint64_t(guardHandle),
		free:   C.UniffiForeignFutureDroppedCallback(C.iroh_uniffiFreeGorutine),
	}

	// Wait for compleation or cancel
	go func() {
		select {
		case <-cancel:
		case res := <-result:
			C.call_UniffiForeignFutureCompleteVoid(uniffiFutureCallback, uniffiCallbackData, res)
		}
	}()

	// Eval callback asynchroniously
	go func() {
		asyncResult := &C.UniffiForeignFutureResultVoid{}
		callStatus := &asyncResult.callStatus
		defer func() {
			result <- *asyncResult
		}()

		err :=
			uniffiObj.Accept(
				FfiConverterConnectionINSTANCE.Lift(conn),
			)

		if err != nil {
			var actualError *CallbackError
			if errors.As(err, &actualError) {
				*callStatus = C.RustCallStatus{
					code:     C.int8_t(uniffiCallbackResultError),
					errorBuf: FfiConverterCallbackErrorINSTANCE.Lower(actualError),
				}
			} else {
				*callStatus = C.RustCallStatus{
					code: C.int8_t(uniffiCallbackUnexpectedResultError),
				}
			}
			return
		}

	}()
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerMethod1
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerMethod1(uniffiHandle C.uint64_t, uniffiFutureCallback C.UniffiForeignFutureCompleteVoid, uniffiCallbackData C.uint64_t, uniffiOutDroppedCallback *C.UniffiForeignFutureDroppedCallbackStruct) {
	handle := uint64(uniffiHandle)
	uniffiObj, ok := FfiConverterProtocolHandlerINSTANCE.handleMap.tryGet(handle)
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}

	result := make(chan C.UniffiForeignFutureResultVoid, 1)
	cancel := make(chan struct{}, 1)
	guardHandle := cgo.NewHandle(cancel)
	*uniffiOutDroppedCallback = C.UniffiForeignFutureDroppedCallbackStruct{
		handle: C.uint64_t(guardHandle),
		free:   C.UniffiForeignFutureDroppedCallback(C.iroh_uniffiFreeGorutine),
	}

	// Wait for compleation or cancel
	go func() {
		select {
		case <-cancel:
		case res := <-result:
			C.call_UniffiForeignFutureCompleteVoid(uniffiFutureCallback, uniffiCallbackData, res)
		}
	}()

	// Eval callback asynchroniously
	go func() {
		asyncResult := &C.UniffiForeignFutureResultVoid{}
		defer func() {
			result <- *asyncResult
		}()

		uniffiObj.Shutdown()

	}()
}

var UniffiVTableCallbackInterfaceProtocolHandlerINSTANCE = C.UniffiVTableCallbackInterfaceProtocolHandler{
	uniffiFree:  (C.UniffiCallbackInterfaceFree)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerFree),
	uniffiClone: (C.UniffiCallbackInterfaceClone)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerClone),
	accept:      (C.UniffiCallbackInterfaceProtocolHandlerMethod0)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerMethod0),
	shutdown:    (C.UniffiCallbackInterfaceProtocolHandlerMethod1)(C.iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerMethod1),
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerFree
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerFree(handle C.uint64_t) {
	FfiConverterProtocolHandlerINSTANCE.handleMap.remove(uint64(handle))
}

//export iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerClone
func iroh_ffi_endpoint_cgo_dispatchCallbackInterfaceProtocolHandlerClone(handle C.uint64_t) C.uint64_t {
	val, ok := FfiConverterProtocolHandlerINSTANCE.handleMap.tryGet(uint64(handle))
	if !ok {
		panic(fmt.Errorf("no callback in handle map: %d", handle))
	}
	return C.uint64_t(FfiConverterProtocolHandlerINSTANCE.handleMap.insert(val))
}

func (c FfiConverterProtocolHandler) register() {
	C.uniffi_iroh_ffi_fn_init_callback_vtable_protocolhandler(&UniffiVTableCallbackInterfaceProtocolHandlerINSTANCE)
}

// The incoming half of a QUIC stream.
type RecvStreamInterface interface {
	// Total bytes read from this stream so far.
	BytesRead() (uint64, error)
	Id() string
	// Read up to `size_limit` bytes into a fresh buffer.
	Read(sizeLimit uint32) ([]byte, error)
	// Read exactly `size` bytes, erroring if the stream ends early.
	ReadExact(size uint32) ([]byte, error)
	// Read until end-of-stream, with `size_limit` as a maximum.
	ReadToEnd(sizeLimit uint32) ([]byte, error)
	ReceivedReset() (*uint64, error)
	// Stop the incoming stream with an error code.
	Stop(errorCode uint64) error
}

// The incoming half of a QUIC stream.
type RecvStream struct {
	ffiObject FfiObject
}

// Total bytes read from this stream so far.
func (_self *RecvStream) BytesRead() (uint64, error) {
	_pointer := _self.ffiObject.incrementPointer("*RecvStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) uint64 {
			return FfiConverterUint64INSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_recvstream_bytes_read(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

func (_self *RecvStream) Id() string {
	_pointer := _self.ffiObject.incrementPointer("*RecvStream")
	defer _self.ffiObject.decrementPointer()
	res, _ := uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) string {
			return FfiConverterStringINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_recvstream_id(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	return res
}

// Read up to `size_limit` bytes into a fresh buffer.
func (_self *RecvStream) Read(sizeLimit uint32) ([]byte, error) {
	_pointer := _self.ffiObject.incrementPointer("*RecvStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) []byte {
			return FfiConverterBytesINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_recvstream_read(
			_pointer, FfiConverterUint32INSTANCE.Lower(sizeLimit)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Read exactly `size` bytes, erroring if the stream ends early.
func (_self *RecvStream) ReadExact(size uint32) ([]byte, error) {
	_pointer := _self.ffiObject.incrementPointer("*RecvStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) []byte {
			return FfiConverterBytesINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_recvstream_read_exact(
			_pointer, FfiConverterUint32INSTANCE.Lower(size)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Read until end-of-stream, with `size_limit` as a maximum.
func (_self *RecvStream) ReadToEnd(sizeLimit uint32) ([]byte, error) {
	_pointer := _self.ffiObject.incrementPointer("*RecvStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) []byte {
			return FfiConverterBytesINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_recvstream_read_to_end(
			_pointer, FfiConverterUint32INSTANCE.Lower(sizeLimit)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

func (_self *RecvStream) ReceivedReset() (*uint64, error) {
	_pointer := _self.ffiObject.incrementPointer("*RecvStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) *uint64 {
			return FfiConverterOptionalUint64INSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_recvstream_received_reset(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Stop the incoming stream with an error code.
func (_self *RecvStream) Stop(errorCode uint64) error {
	_pointer := _self.ffiObject.incrementPointer("*RecvStream")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_recvstream_stop(
			_pointer, FfiConverterUint64INSTANCE.Lower(errorCode)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *RecvStream) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterRecvStream struct{}

var FfiConverterRecvStreamINSTANCE = FfiConverterRecvStream{}

func (c FfiConverterRecvStream) Lift(handle C.uint64_t) *RecvStream {
	result := &RecvStream{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_recvstream(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_recvstream(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*RecvStream).Destroy)
	return result
}

func (c FfiConverterRecvStream) Read(reader io.Reader) *RecvStream {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterRecvStream) Lower(value *RecvStream) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*RecvStream")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterRecvStream) Write(writer io.Writer, value *RecvStream) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalRecvStream(handle uint64) *RecvStream {
	return FfiConverterRecvStreamINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalRecvStream(value *RecvStream) uint64 {
	return uint64(FfiConverterRecvStreamINSTANCE.Lower(value))
}

type FfiDestroyerRecvStream struct{}

func (_ FfiDestroyerRecvStream) Destroy(value *RecvStream) {
	value.Destroy()
}

// A collection of relay servers an endpoint should consider.
//
// Mirrors `iroh::RelayMap`. Construct with [`Self::empty`] or [`Self::from_urls`]
// and mutate with [`Self::insert`] / [`Self::remove`].
type RelayMapInterface interface {
	// Check whether the given relay URL is in the map.
	Contains(url string) (bool, error)
	// Look up the configuration for the given relay URL.
	Get(url string) (*RelayConfig, error)
	// Insert a relay (replacing any prior entry for the same URL).
	Insert(config RelayConfig) error
	// True if the map has no relays.
	IsEmpty() bool
	// Number of relays in the map.
	Len() uint32
	// Remove the entry for the given relay URL. Returns true if something was
	// removed.
	Remove(url string) (bool, error)
	// All relay URLs currently in the map.
	Urls() []string
}

// A collection of relay servers an endpoint should consider.
//
// Mirrors `iroh::RelayMap`. Construct with [`Self::empty`] or [`Self::from_urls`]
// and mutate with [`Self::insert`] / [`Self::remove`].
type RelayMap struct {
	ffiObject FfiObject
}

// Create an empty relay map.
func RelayMapEmpty() *RelayMap {
	return FfiConverterRelayMapINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_relaymap_empty(_uniffiStatus)
	}))
}

// Build a relay map from a list of relay URLs (each becomes a default
// [`RelayConfig`]).
func RelayMapFromUrls(urls []string) (*RelayMap, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_relaymap_from_urls(FfiConverterSequenceStringINSTANCE.Lower(urls), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RelayMap
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRelayMapINSTANCE.Lift(_uniffiRV), nil
	}
}

// Check whether the given relay URL is in the map.
func (_self *RelayMap) Contains(url string) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_relaymap_contains(
			_pointer, FfiConverterStringINSTANCE.Lower(url), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// Look up the configuration for the given relay URL.
func (_self *RelayMap) Get(url string) (*RelayConfig, error) {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_relaymap_get(
				_pointer, FfiConverterStringINSTANCE.Lower(url), _uniffiStatus),
		}
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RelayConfig
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterOptionalRelayConfigINSTANCE.Lift(_uniffiRV), nil
	}
}

// Insert a relay (replacing any prior entry for the same URL).
func (_self *RelayMap) Insert(config RelayConfig) error {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	_, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_method_relaymap_insert(
			_pointer, FfiConverterRelayConfigINSTANCE.Lower(config), _uniffiStatus)
		return false
	})
	return _uniffiErr.AsError()
}

// True if the map has no relays.
func (_self *RelayMap) IsEmpty() bool {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_relaymap_is_empty(
			_pointer, _uniffiStatus)
	}))
}

// Number of relays in the map.
func (_self *RelayMap) Len() uint32 {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint32INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint32_t {
		return C.uniffi_iroh_ffi_fn_method_relaymap_len(
			_pointer, _uniffiStatus)
	}))
}

// Remove the entry for the given relay URL. Returns true if something was
// removed.
func (_self *RelayMap) Remove(url string) (bool, error) {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_relaymap_remove(
			_pointer, FfiConverterStringINSTANCE.Lower(url), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue bool
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterBoolINSTANCE.Lift(_uniffiRV), nil
	}
}

// All relay URLs currently in the map.
func (_self *RelayMap) Urls() []string {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSequenceStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_relaymap_urls(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *RelayMap) String() string {
	_pointer := _self.ffiObject.incrementPointer("*RelayMap")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_relaymap_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (object *RelayMap) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterRelayMap struct{}

var FfiConverterRelayMapINSTANCE = FfiConverterRelayMap{}

func (c FfiConverterRelayMap) Lift(handle C.uint64_t) *RelayMap {
	result := &RelayMap{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_relaymap(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_relaymap(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*RelayMap).Destroy)
	return result
}

func (c FfiConverterRelayMap) Read(reader io.Reader) *RelayMap {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterRelayMap) Lower(value *RelayMap) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*RelayMap")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterRelayMap) Write(writer io.Writer, value *RelayMap) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalRelayMap(handle uint64) *RelayMap {
	return FfiConverterRelayMapINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalRelayMap(value *RelayMap) uint64 {
	return uint64(FfiConverterRelayMapINSTANCE.Lower(value))
}

type FfiDestroyerRelayMap struct{}

func (_ FfiDestroyerRelayMap) Destroy(value *RelayMap) {
	value.Destroy()
}

// Configuration for which relay servers an endpoint uses.
//
// Mirrors `iroh::RelayMode`. Use one of the constructors below.
type RelayModeInterface interface {
	// The relay map this mode resolves to.
	RelayMap() *RelayMap
}

// Configuration for which relay servers an endpoint uses.
//
// Mirrors `iroh::RelayMode`. Use one of the constructors below.
type RelayMode struct {
	ffiObject FfiObject
}

// Use a custom relay map.
func RelayModeCustom(varMap *RelayMap) *RelayMode {
	return FfiConverterRelayModeINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_relaymode_custom(FfiConverterRelayMapINSTANCE.Lower(varMap), _uniffiStatus)
	}))
}

// Build a custom relay mode directly from a list of relay URLs.
func RelayModeCustomFromUrls(urls []string) (*RelayMode, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_relaymode_custom_from_urls(FfiConverterSequenceStringINSTANCE.Lower(urls), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *RelayMode
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterRelayModeINSTANCE.Lift(_uniffiRV), nil
	}
}

// Use the n0 production relay map.
func RelayModeDefaultMode() *RelayMode {
	return FfiConverterRelayModeINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_relaymode_default_mode(_uniffiStatus)
	}))
}

// No relays — listening and dialing via relay are both disabled.
func RelayModeDisabled() *RelayMode {
	return FfiConverterRelayModeINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_relaymode_disabled(_uniffiStatus)
	}))
}

// Use the n0 staging relay map.
func RelayModeStaging() *RelayMode {
	return FfiConverterRelayModeINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_relaymode_staging(_uniffiStatus)
	}))
}

// The relay map this mode resolves to.
func (_self *RelayMode) RelayMap() *RelayMap {
	_pointer := _self.ffiObject.incrementPointer("*RelayMode")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterRelayMapINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_relaymode_relay_map(
			_pointer, _uniffiStatus)
	}))
}

func (_self *RelayMode) String() string {
	_pointer := _self.ffiObject.incrementPointer("*RelayMode")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_relaymode_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (object *RelayMode) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterRelayMode struct{}

var FfiConverterRelayModeINSTANCE = FfiConverterRelayMode{}

func (c FfiConverterRelayMode) Lift(handle C.uint64_t) *RelayMode {
	result := &RelayMode{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_relaymode(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_relaymode(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*RelayMode).Destroy)
	return result
}

func (c FfiConverterRelayMode) Read(reader io.Reader) *RelayMode {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterRelayMode) Lower(value *RelayMode) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*RelayMode")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterRelayMode) Write(writer io.Writer, value *RelayMode) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalRelayMode(handle uint64) *RelayMode {
	return FfiConverterRelayModeINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalRelayMode(value *RelayMode) uint64 {
	return uint64(FfiConverterRelayModeINSTANCE.Lower(value))
}

type FfiDestroyerRelayMode struct{}

func (_ FfiDestroyerRelayMode) Destroy(value *RelayMode) {
	value.Destroy()
}

// The secret key half of an endpoint identity.
//
// Mirrors `iroh::SecretKey`. Used internally by [`Endpoint`](crate::Endpoint) to
// produce its TLS certificate and to sign arbitrary messages.
type SecretKeyInterface interface {
	// The public [`EndpointId`] derived from this secret key.
	Public() *EndpointId
	// Sign a message, producing an ed25519 signature.
	Sign(message []byte) *Signature
	// Get the underlying 32 bytes of the secret key.
	ToBytes() []byte
}

// The secret key half of an endpoint identity.
//
// Mirrors `iroh::SecretKey`. Used internally by [`Endpoint`](crate::Endpoint) to
// produce its TLS certificate and to sign arbitrary messages.
type SecretKey struct {
	ffiObject FfiObject
}

// Construct a [`SecretKey`] from raw bytes.
func SecretKeyFromBytes(bytes []byte) (*SecretKey, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_secretkey_from_bytes(FfiConverterBytesINSTANCE.Lower(bytes), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *SecretKey
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterSecretKeyINSTANCE.Lift(_uniffiRV), nil
	}
}

// Generate a new random secret key.
func SecretKeyGenerate() *SecretKey {
	return FfiConverterSecretKeyINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_secretkey_generate(_uniffiStatus)
	}))
}

// The public [`EndpointId`] derived from this secret key.
func (_self *SecretKey) Public() *EndpointId {
	_pointer := _self.ffiObject.incrementPointer("*SecretKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterEndpointIdINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_secretkey_public(
			_pointer, _uniffiStatus)
	}))
}

// Sign a message, producing an ed25519 signature.
func (_self *SecretKey) Sign(message []byte) *Signature {
	_pointer := _self.ffiObject.incrementPointer("*SecretKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterSignatureINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_secretkey_sign(
			_pointer, FfiConverterBytesINSTANCE.Lower(message), _uniffiStatus)
	}))
}

// Get the underlying 32 bytes of the secret key.
func (_self *SecretKey) ToBytes() []byte {
	_pointer := _self.ffiObject.incrementPointer("*SecretKey")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBytesINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_secretkey_to_bytes(
				_pointer, _uniffiStatus),
		}
	}))
}
func (object *SecretKey) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterSecretKey struct{}

var FfiConverterSecretKeyINSTANCE = FfiConverterSecretKey{}

func (c FfiConverterSecretKey) Lift(handle C.uint64_t) *SecretKey {
	result := &SecretKey{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_secretkey(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_secretkey(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*SecretKey).Destroy)
	return result
}

func (c FfiConverterSecretKey) Read(reader io.Reader) *SecretKey {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterSecretKey) Lower(value *SecretKey) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*SecretKey")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterSecretKey) Write(writer io.Writer, value *SecretKey) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalSecretKey(handle uint64) *SecretKey {
	return FfiConverterSecretKeyINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalSecretKey(value *SecretKey) uint64 {
	return uint64(FfiConverterSecretKeyINSTANCE.Lower(value))
}

type FfiDestroyerSecretKey struct{}

func (_ FfiDestroyerSecretKey) Destroy(value *SecretKey) {
	value.Destroy()
}

// The outgoing half of a QUIC stream.
type SendStreamInterface interface {
	// Signal that no more data will be sent on this stream.
	Finish() error
	Id() string
	Priority() (int32, error)
	// Abort the stream with the given error code.
	Reset(errorCode uint64) error
	SetPriority(p int32) error
	Stopped() (*uint64, error)
	// Write some bytes, returning the number actually written.
	Write(buf []byte) (uint64, error)
	// Write all bytes, looping as needed.
	WriteAll(buf []byte) error
}

// The outgoing half of a QUIC stream.
type SendStream struct {
	ffiObject FfiObject
}

// Signal that no more data will be sent on this stream.
func (_self *SendStream) Finish() error {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_sendstream_finish(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

func (_self *SendStream) Id() string {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	res, _ := uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) string {
			return FfiConverterStringINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_sendstream_id(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	return res
}

func (_self *SendStream) Priority() (int32, error) {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.int32_t {
			res := C.ffi_iroh_ffi_rust_future_complete_i32(handle, status)
			return res
		},
		// liftFn
		func(ffi C.int32_t) int32 {
			return FfiConverterInt32INSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_sendstream_priority(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_i32(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_i32(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Abort the stream with the given error code.
func (_self *SendStream) Reset(errorCode uint64) error {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_sendstream_reset(
			_pointer, FfiConverterUint64INSTANCE.Lower(errorCode)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

func (_self *SendStream) SetPriority(p int32) error {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_sendstream_set_priority(
			_pointer, FfiConverterInt32INSTANCE.Lower(p)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

func (_self *SendStream) Stopped() (*uint64, error) {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) *uint64 {
			return FfiConverterOptionalUint64INSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_sendstream_stopped(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Write some bytes, returning the number actually written.
func (_self *SendStream) Write(buf []byte) (uint64, error) {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) uint64 {
			return FfiConverterUint64INSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_sendstream_write(
			_pointer, FfiConverterBytesINSTANCE.Lower(buf)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Write all bytes, looping as needed.
func (_self *SendStream) WriteAll(buf []byte) error {
	_pointer := _self.ffiObject.incrementPointer("*SendStream")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_sendstream_write_all(
			_pointer, FfiConverterBytesINSTANCE.Lower(buf)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}
func (object *SendStream) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterSendStream struct{}

var FfiConverterSendStreamINSTANCE = FfiConverterSendStream{}

func (c FfiConverterSendStream) Lift(handle C.uint64_t) *SendStream {
	result := &SendStream{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_sendstream(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_sendstream(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*SendStream).Destroy)
	return result
}

func (c FfiConverterSendStream) Read(reader io.Reader) *SendStream {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterSendStream) Lower(value *SendStream) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*SendStream")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterSendStream) Write(writer io.Writer, value *SendStream) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalSendStream(handle uint64) *SendStream {
	return FfiConverterSendStreamINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalSendStream(value *SendStream) uint64 {
	return uint64(FfiConverterSendStreamINSTANCE.Lower(value))
}

type FfiDestroyerSendStream struct{}

func (_ FfiDestroyerSendStream) Destroy(value *SendStream) {
	value.Destroy()
}

// Client for services.iroh.computer.
//
// Construct with [`Self::create`]; metrics are pushed automatically while the
// client is alive. Drop the client (or let it go out of scope) to stop.
type ServicesClientInterface interface {
	// Read the current endpoint name from the local client.
	Name() (*string, error)
	// Ping the remote service to confirm connectivity.
	Ping() error
	// Push the current metrics snapshot now. (Metrics are also pushed on the
	// interval configured at build time; this lets you force a flush.)
	PushMetrics() error
	// Set the endpoint name cloud-side. Must be 2–128 UTF-8 bytes.
	SetName(name string) error
	// Run a local network-diagnostics report. When `send` is true the report
	// is also submitted to iroh-services for storage.
	SubmitNetworkDiagnostics(send bool) (DiagnosticsSummary, error)
}

// Client for services.iroh.computer.
//
// Construct with [`Self::create`]; metrics are pushed automatically while the
// client is alive. Drop the client (or let it go out of scope) to stop.
type ServicesClient struct {
	ffiObject FfiObject
}

// Build a new client bound to the given endpoint.
func ServicesClientCreate(endpoint *Endpoint, options ServicesOptions) (*ServicesClient, error) {
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
			res := C.ffi_iroh_ffi_rust_future_complete_u64(handle, status)
			return res
		},
		// liftFn
		func(ffi C.uint64_t) *ServicesClient {
			return FfiConverterServicesClientINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_constructor_servicesclient_create(FfiConverterEndpointINSTANCE.Lower(endpoint), FfiConverterServicesOptionsINSTANCE.Lower(options)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_u64(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_u64(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Read the current endpoint name from the local client.
func (_self *ServicesClient) Name() (*string, error) {
	_pointer := _self.ffiObject.incrementPointer("*ServicesClient")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) *string {
			return FfiConverterOptionalStringINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_servicesclient_name(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}

// Ping the remote service to confirm connectivity.
func (_self *ServicesClient) Ping() error {
	_pointer := _self.ffiObject.incrementPointer("*ServicesClient")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_servicesclient_ping(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// Push the current metrics snapshot now. (Metrics are also pushed on the
// interval configured at build time; this lets you force a flush.)
func (_self *ServicesClient) PushMetrics() error {
	_pointer := _self.ffiObject.incrementPointer("*ServicesClient")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_servicesclient_push_metrics(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// Set the endpoint name cloud-side. Must be 2–128 UTF-8 bytes.
func (_self *ServicesClient) SetName(name string) error {
	_pointer := _self.ffiObject.incrementPointer("*ServicesClient")
	defer _self.ffiObject.decrementPointer()
	_, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_servicesclient_set_name(
			_pointer, FfiConverterStringINSTANCE.Lower(name)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

	if err == nil {
		return nil
	}

	return err
}

// Run a local network-diagnostics report. When `send` is true the report
// is also submitted to iroh-services for storage.
func (_self *ServicesClient) SubmitNetworkDiagnostics(send bool) (DiagnosticsSummary, error) {
	_pointer := _self.ffiObject.incrementPointer("*ServicesClient")
	defer _self.ffiObject.decrementPointer()
	res, err := uniffiRustCallAsync[*IrohError](
		FfiConverterIrohErrorINSTANCE,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) RustBufferI {
			res := C.ffi_iroh_ffi_rust_future_complete_rust_buffer(handle, status)
			return GoRustBuffer{
				inner: res,
			}
		},
		// liftFn
		func(ffi RustBufferI) DiagnosticsSummary {
			return FfiConverterDiagnosticsSummaryINSTANCE.Lift(ffi)
		},
		C.uniffi_iroh_ffi_fn_method_servicesclient_submit_network_diagnostics(
			_pointer, FfiConverterBoolINSTANCE.Lower(send)),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_rust_buffer(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_rust_buffer(handle)
		},
	)

	if err == nil {
		return res, nil
	}

	return res, err
}
func (object *ServicesClient) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterServicesClient struct{}

var FfiConverterServicesClientINSTANCE = FfiConverterServicesClient{}

func (c FfiConverterServicesClient) Lift(handle C.uint64_t) *ServicesClient {
	result := &ServicesClient{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_servicesclient(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_servicesclient(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*ServicesClient).Destroy)
	return result
}

func (c FfiConverterServicesClient) Read(reader io.Reader) *ServicesClient {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterServicesClient) Lower(value *ServicesClient) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*ServicesClient")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterServicesClient) Write(writer io.Writer, value *ServicesClient) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalServicesClient(handle uint64) *ServicesClient {
	return FfiConverterServicesClientINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalServicesClient(value *ServicesClient) uint64 {
	return uint64(FfiConverterServicesClientINSTANCE.Lower(value))
}

type FfiDestroyerServicesClient struct{}

func (_ FfiDestroyerServicesClient) Destroy(value *ServicesClient) {
	value.Destroy()
}

// An ed25519 signature over a message.
type SignatureInterface interface {
	// Get the underlying 64 bytes.
	ToBytes() []byte
}

// An ed25519 signature over a message.
type Signature struct {
	ffiObject FfiObject
}

// Construct a [`Signature`] from raw bytes (64 bytes).
func SignatureFromBytes(bytes []byte) (*Signature, error) {
	_uniffiRV, _uniffiErr := rustCallWithError[*IrohError](FfiConverterIrohError{}, func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_constructor_signature_from_bytes(FfiConverterBytesINSTANCE.Lower(bytes), _uniffiStatus)
	})
	if _uniffiErr != nil {
		var _uniffiDefaultValue *Signature
		return _uniffiDefaultValue, _uniffiErr
	} else {
		return FfiConverterSignatureINSTANCE.Lift(_uniffiRV), nil
	}
}

// Get the underlying 64 bytes.
func (_self *Signature) ToBytes() []byte {
	_pointer := _self.ffiObject.incrementPointer("*Signature")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBytesINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_signature_to_bytes(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Signature) String() string {
	_pointer := _self.ffiObject.incrementPointer("*Signature")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterStringINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) RustBufferI {
		return GoRustBuffer{
			inner: C.uniffi_iroh_ffi_fn_method_signature_uniffi_trait_display(
				_pointer, _uniffiStatus),
		}
	}))
}

func (_self *Signature) Eq(other *Signature) bool {
	_pointer := _self.ffiObject.incrementPointer("*Signature")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_signature_uniffi_trait_eq_eq(
			_pointer, FfiConverterSignatureINSTANCE.Lower(other), _uniffiStatus)
	}))
}

func (_self *Signature) Ne(other *Signature) bool {
	_pointer := _self.ffiObject.incrementPointer("*Signature")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterBoolINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.int8_t {
		return C.uniffi_iroh_ffi_fn_method_signature_uniffi_trait_eq_ne(
			_pointer, FfiConverterSignatureINSTANCE.Lower(other), _uniffiStatus)
	}))
}

func (_self *Signature) Hash() uint64 {
	_pointer := _self.ffiObject.incrementPointer("*Signature")
	defer _self.ffiObject.decrementPointer()
	return FfiConverterUint64INSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_method_signature_uniffi_trait_hash(
			_pointer, _uniffiStatus)
	}))
}

func (object *Signature) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterSignature struct{}

var FfiConverterSignatureINSTANCE = FfiConverterSignature{}

func (c FfiConverterSignature) Lift(handle C.uint64_t) *Signature {
	result := &Signature{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_signature(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_signature(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*Signature).Destroy)
	return result
}

func (c FfiConverterSignature) Read(reader io.Reader) *Signature {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterSignature) Lower(value *Signature) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*Signature")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterSignature) Write(writer io.Writer, value *Signature) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalSignature(handle uint64) *Signature {
	return FfiConverterSignatureINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalSignature(value *Signature) uint64 {
	return uint64(FfiConverterSignatureINSTANCE.Lower(value))
}

type FfiDestroyerSignature struct{}

func (_ FfiDestroyerSignature) Destroy(value *Signature) {
	value.Destroy()
}

// Handle to a running watcher task. Drop it (or call [`Self::stop`]) to
// unregister the callback.
type WatchHandleInterface interface {
	// Stop the watcher, aborting the background task.
	Stop()
}

// Handle to a running watcher task. Drop it (or call [`Self::stop`]) to
// unregister the callback.
type WatchHandle struct {
	ffiObject FfiObject
}

// Stop the watcher, aborting the background task.
func (_self *WatchHandle) Stop() {
	_pointer := _self.ffiObject.incrementPointer("*WatchHandle")
	defer _self.ffiObject.decrementPointer()
	uniffiRustCallAsync[error](
		nil,
		// completeFn
		func(handle C.uint64_t, status *C.RustCallStatus) struct{} {
			C.ffi_iroh_ffi_rust_future_complete_void(handle, status)
			return struct{}{}
		},
		// liftFn
		func(_ struct{}) struct{} { return struct{}{} },
		C.uniffi_iroh_ffi_fn_method_watchhandle_stop(
			_pointer),
		// pollFn
		func(handle C.uint64_t, continuation C.UniffiRustFutureContinuationCallback, data C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_poll_void(handle, continuation, data)
		},
		// freeFn
		func(handle C.uint64_t) {
			C.ffi_iroh_ffi_rust_future_free_void(handle)
		},
	)

}
func (object *WatchHandle) Destroy() {
	runtime.SetFinalizer(object, nil)
	object.ffiObject.destroy()
}

type FfiConverterWatchHandle struct{}

var FfiConverterWatchHandleINSTANCE = FfiConverterWatchHandle{}

func (c FfiConverterWatchHandle) Lift(handle C.uint64_t) *WatchHandle {
	result := &WatchHandle{
		newFfiObject(
			handle,
			func(handle C.uint64_t, status *C.RustCallStatus) C.uint64_t {
				return C.uniffi_iroh_ffi_fn_clone_watchhandle(handle, status)
			},
			func(handle C.uint64_t, status *C.RustCallStatus) {
				C.uniffi_iroh_ffi_fn_free_watchhandle(handle, status)
			},
		),
	}
	runtime.SetFinalizer(result, (*WatchHandle).Destroy)
	return result
}

func (c FfiConverterWatchHandle) Read(reader io.Reader) *WatchHandle {
	return c.Lift(C.uint64_t(readUint64(reader)))
}

func (c FfiConverterWatchHandle) Lower(value *WatchHandle) C.uint64_t {
	// TODO: this is bad - all synchronization from ObjectRuntime.go is discarded here,
	// because the handle will be decremented immediately after this function returns,
	// and someone will be left holding onto a non-locked handle.
	handle := value.ffiObject.incrementPointer("*WatchHandle")
	defer value.ffiObject.decrementPointer()
	return handle
}

func (c FfiConverterWatchHandle) Write(writer io.Writer, value *WatchHandle) {
	writeUint64(writer, uint64(c.Lower(value)))
}

func LiftFromExternalWatchHandle(handle uint64) *WatchHandle {
	return FfiConverterWatchHandleINSTANCE.Lift(C.uint64_t(handle))
}

func LowerToExternalWatchHandle(value *WatchHandle) uint64 {
	return uint64(FfiConverterWatchHandleINSTANCE.Lower(value))
}

type FfiDestroyerWatchHandle struct{}

func (_ FfiDestroyerWatchHandle) Destroy(value *WatchHandle) {
	value.Destroy()
}

// Flat snapshot of the headline numbers from `noq::ConnectionStats`.
//
// Counters are `i64` (not `u64`) so Kotlin sees `Long`, not `ULong`.
type ConnectionStats struct {
	// Total UDP datagrams transmitted.
	UdpTxDatagrams int64
	// Total UDP bytes transmitted.
	UdpTxBytes int64
	// Total UDP datagrams received.
	UdpRxDatagrams int64
	// Total UDP bytes received.
	UdpRxBytes int64
	// Total packets considered lost.
	LostPackets int64
	// Total bytes considered lost.
	LostBytes int64
}

func (r *ConnectionStats) Destroy() {
	FfiDestroyerInt64{}.Destroy(r.UdpTxDatagrams)
	FfiDestroyerInt64{}.Destroy(r.UdpTxBytes)
	FfiDestroyerInt64{}.Destroy(r.UdpRxDatagrams)
	FfiDestroyerInt64{}.Destroy(r.UdpRxBytes)
	FfiDestroyerInt64{}.Destroy(r.LostPackets)
	FfiDestroyerInt64{}.Destroy(r.LostBytes)
}

type FfiConverterConnectionStats struct{}

var FfiConverterConnectionStatsINSTANCE = FfiConverterConnectionStats{}

func (c FfiConverterConnectionStats) Lift(rb RustBufferI) ConnectionStats {
	return LiftFromRustBuffer[ConnectionStats](c, rb)
}

func (c FfiConverterConnectionStats) Read(reader io.Reader) ConnectionStats {
	return ConnectionStats{
		FfiConverterInt64INSTANCE.Read(reader),
		FfiConverterInt64INSTANCE.Read(reader),
		FfiConverterInt64INSTANCE.Read(reader),
		FfiConverterInt64INSTANCE.Read(reader),
		FfiConverterInt64INSTANCE.Read(reader),
		FfiConverterInt64INSTANCE.Read(reader),
	}
}

func (c FfiConverterConnectionStats) Lower(value ConnectionStats) C.RustBuffer {
	return LowerIntoRustBuffer[ConnectionStats](c, value)
}

func (c FfiConverterConnectionStats) LowerExternal(value ConnectionStats) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ConnectionStats](c, value))
}

func (c FfiConverterConnectionStats) Write(writer io.Writer, value ConnectionStats) {
	FfiConverterInt64INSTANCE.Write(writer, value.UdpTxDatagrams)
	FfiConverterInt64INSTANCE.Write(writer, value.UdpTxBytes)
	FfiConverterInt64INSTANCE.Write(writer, value.UdpRxDatagrams)
	FfiConverterInt64INSTANCE.Write(writer, value.UdpRxBytes)
	FfiConverterInt64INSTANCE.Write(writer, value.LostPackets)
	FfiConverterInt64INSTANCE.Write(writer, value.LostBytes)
}

type FfiDestroyerConnectionStats struct{}

func (_ FfiDestroyerConnectionStats) Destroy(value ConnectionStats) {
	value.Destroy()
}

// A snapshot value for a single endpoint metric.
type CounterStats struct {
	// The counter / gauge value.
	Value uint32
	// The metric description.
	Description string
}

func (r *CounterStats) Destroy() {
	FfiDestroyerUint32{}.Destroy(r.Value)
	FfiDestroyerString{}.Destroy(r.Description)
}

type FfiConverterCounterStats struct{}

var FfiConverterCounterStatsINSTANCE = FfiConverterCounterStats{}

func (c FfiConverterCounterStats) Lift(rb RustBufferI) CounterStats {
	return LiftFromRustBuffer[CounterStats](c, rb)
}

func (c FfiConverterCounterStats) Read(reader io.Reader) CounterStats {
	return CounterStats{
		FfiConverterUint32INSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterCounterStats) Lower(value CounterStats) C.RustBuffer {
	return LowerIntoRustBuffer[CounterStats](c, value)
}

func (c FfiConverterCounterStats) LowerExternal(value CounterStats) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[CounterStats](c, value))
}

func (c FfiConverterCounterStats) Write(writer io.Writer, value CounterStats) {
	FfiConverterUint32INSTANCE.Write(writer, value.Value)
	FfiConverterStringINSTANCE.Write(writer, value.Description)
}

type FfiDestroyerCounterStats struct{}

func (_ FfiDestroyerCounterStats) Destroy(value CounterStats) {
	value.Destroy()
}

// Flattened summary of an `iroh_services::net_diagnostics::DiagnosticsReport`.
//
// Net-report and portmap details are dropped from the FFI surface (they have
// deep, non-uniffi-friendly shapes); use the iroh-services dashboard to read
// the full report after `submit_network_diagnostics(send=true)`.
type DiagnosticsSummary struct {
	// Endpoint id of the local endpoint.
	EndpointId string
	// Direct addresses (ip:port) that the endpoint reports.
	DirectAddrs []string
	// iroh crate version this report was produced with.
	IrohVersion string
	// iroh-services crate version this report was produced with.
	IrohServicesVersion string
	// True if the local net-report probe returned a result.
	HasNetReport bool
	// UPnP availability, if a portmap probe was run.
	Upnp *bool
	// PCP availability, if a portmap probe was run.
	Pcp *bool
	// NAT-PMP availability, if a portmap probe was run.
	NatPmp *bool
}

func (r *DiagnosticsSummary) Destroy() {
	FfiDestroyerString{}.Destroy(r.EndpointId)
	FfiDestroyerSequenceString{}.Destroy(r.DirectAddrs)
	FfiDestroyerString{}.Destroy(r.IrohVersion)
	FfiDestroyerString{}.Destroy(r.IrohServicesVersion)
	FfiDestroyerBool{}.Destroy(r.HasNetReport)
	FfiDestroyerOptionalBool{}.Destroy(r.Upnp)
	FfiDestroyerOptionalBool{}.Destroy(r.Pcp)
	FfiDestroyerOptionalBool{}.Destroy(r.NatPmp)
}

type FfiConverterDiagnosticsSummary struct{}

var FfiConverterDiagnosticsSummaryINSTANCE = FfiConverterDiagnosticsSummary{}

func (c FfiConverterDiagnosticsSummary) Lift(rb RustBufferI) DiagnosticsSummary {
	return LiftFromRustBuffer[DiagnosticsSummary](c, rb)
}

func (c FfiConverterDiagnosticsSummary) Read(reader io.Reader) DiagnosticsSummary {
	return DiagnosticsSummary{
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterSequenceStringINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterOptionalBoolINSTANCE.Read(reader),
		FfiConverterOptionalBoolINSTANCE.Read(reader),
		FfiConverterOptionalBoolINSTANCE.Read(reader),
	}
}

func (c FfiConverterDiagnosticsSummary) Lower(value DiagnosticsSummary) C.RustBuffer {
	return LowerIntoRustBuffer[DiagnosticsSummary](c, value)
}

func (c FfiConverterDiagnosticsSummary) LowerExternal(value DiagnosticsSummary) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[DiagnosticsSummary](c, value))
}

func (c FfiConverterDiagnosticsSummary) Write(writer io.Writer, value DiagnosticsSummary) {
	FfiConverterStringINSTANCE.Write(writer, value.EndpointId)
	FfiConverterSequenceStringINSTANCE.Write(writer, value.DirectAddrs)
	FfiConverterStringINSTANCE.Write(writer, value.IrohVersion)
	FfiConverterStringINSTANCE.Write(writer, value.IrohServicesVersion)
	FfiConverterBoolINSTANCE.Write(writer, value.HasNetReport)
	FfiConverterOptionalBoolINSTANCE.Write(writer, value.Upnp)
	FfiConverterOptionalBoolINSTANCE.Write(writer, value.Pcp)
	FfiConverterOptionalBoolINSTANCE.Write(writer, value.NatPmp)
}

type FfiDestroyerDiagnosticsSummary struct{}

func (_ FfiDestroyerDiagnosticsSummary) Destroy(value DiagnosticsSummary) {
	value.Destroy()
}

// Options passed to [`Endpoint::bind`].
type EndpointOptions struct {
	// Preset that configures the endpoint builder. Defaults to [`preset_n0`].
	// Implement the [`Preset`] trait in your language for full control.
	Preset *Preset
	// Override the address the endpoint binds to. Accepts any standard
	// `host:port` form (IPv4 or IPv6).
	BindAddr *string
	// Provide a specific secret key, identifying this endpoint. Must be 32 bytes long.
	SecretKey *[]byte
	// ALPN protocols advertised on the underlying TLS handshake. Independent of
	// the per-protocol handlers in `protocols`; useful for client-only setups
	// or for declaring extra ALPNs.
	Alpns *[][]byte
	// Override which relays the endpoint uses. Defaults to whatever the
	// chosen [`Preset`] configures.
	RelayMode **RelayMode
	// Custom protocols to accept on this endpoint, keyed by ALPN. If provided,
	// an internal router is spawned to dispatch incoming connections to the
	// supplied handlers.
	Protocols *map[string]ProtocolCreator
}

func (r *EndpointOptions) Destroy() {
	FfiDestroyerOptionalPreset{}.Destroy(r.Preset)
	FfiDestroyerOptionalString{}.Destroy(r.BindAddr)
	FfiDestroyerOptionalBytes{}.Destroy(r.SecretKey)
	FfiDestroyerOptionalSequenceBytes{}.Destroy(r.Alpns)
	FfiDestroyerOptionalRelayMode{}.Destroy(r.RelayMode)
	FfiDestroyerOptionalMapBytesProtocolCreator{}.Destroy(r.Protocols)
}

type FfiConverterEndpointOptions struct{}

var FfiConverterEndpointOptionsINSTANCE = FfiConverterEndpointOptions{}

func (c FfiConverterEndpointOptions) Lift(rb RustBufferI) EndpointOptions {
	return LiftFromRustBuffer[EndpointOptions](c, rb)
}

func (c FfiConverterEndpointOptions) Read(reader io.Reader) EndpointOptions {
	return EndpointOptions{
		FfiConverterOptionalPresetINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalBytesINSTANCE.Read(reader),
		FfiConverterOptionalSequenceBytesINSTANCE.Read(reader),
		FfiConverterOptionalRelayModeINSTANCE.Read(reader),
		FfiConverterOptionalMapBytesProtocolCreatorINSTANCE.Read(reader),
	}
}

func (c FfiConverterEndpointOptions) Lower(value EndpointOptions) C.RustBuffer {
	return LowerIntoRustBuffer[EndpointOptions](c, value)
}

func (c FfiConverterEndpointOptions) LowerExternal(value EndpointOptions) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[EndpointOptions](c, value))
}

func (c FfiConverterEndpointOptions) Write(writer io.Writer, value EndpointOptions) {
	FfiConverterOptionalPresetINSTANCE.Write(writer, value.Preset)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.BindAddr)
	FfiConverterOptionalBytesINSTANCE.Write(writer, value.SecretKey)
	FfiConverterOptionalSequenceBytesINSTANCE.Write(writer, value.Alpns)
	FfiConverterOptionalRelayModeINSTANCE.Write(writer, value.RelayMode)
	FfiConverterOptionalMapBytesProtocolCreatorINSTANCE.Write(writer, value.Protocols)
}

type FfiDestroyerEndpointOptions struct{}

func (_ FfiDestroyerEndpointOptions) Destroy(value EndpointOptions) {
	value.Destroy()
}

// A flat snapshot of an open path's state.
type PathSnapshot struct {
	// Opaque path identifier rendered as a string (upstream `PathId` is a u32
	// wrapper but exposes no public accessor).
	Id string
	// True if this path is currently selected for application data.
	IsSelected bool
	// The remote transport address as a string. For IP paths this is
	// `ip:port`; for relay paths this is the relay URL.
	RemoteAddr string
	// True if this is a direct IP path.
	IsIp bool
	// True if this is a relay path.
	IsRelay bool
	// RTT estimate in milliseconds (sampled from the live QUIC state).
	RttMs uint64
	// Flat headline statistics for this path.
	Stats PathStatsRecord
}

func (r *PathSnapshot) Destroy() {
	FfiDestroyerString{}.Destroy(r.Id)
	FfiDestroyerBool{}.Destroy(r.IsSelected)
	FfiDestroyerString{}.Destroy(r.RemoteAddr)
	FfiDestroyerBool{}.Destroy(r.IsIp)
	FfiDestroyerBool{}.Destroy(r.IsRelay)
	FfiDestroyerUint64{}.Destroy(r.RttMs)
	FfiDestroyerPathStatsRecord{}.Destroy(r.Stats)
}

type FfiConverterPathSnapshot struct{}

var FfiConverterPathSnapshotINSTANCE = FfiConverterPathSnapshot{}

func (c FfiConverterPathSnapshot) Lift(rb RustBufferI) PathSnapshot {
	return LiftFromRustBuffer[PathSnapshot](c, rb)
}

func (c FfiConverterPathSnapshot) Read(reader io.Reader) PathSnapshot {
	return PathSnapshot{
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterBoolINSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterPathStatsRecordINSTANCE.Read(reader),
	}
}

func (c FfiConverterPathSnapshot) Lower(value PathSnapshot) C.RustBuffer {
	return LowerIntoRustBuffer[PathSnapshot](c, value)
}

func (c FfiConverterPathSnapshot) LowerExternal(value PathSnapshot) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PathSnapshot](c, value))
}

func (c FfiConverterPathSnapshot) Write(writer io.Writer, value PathSnapshot) {
	FfiConverterStringINSTANCE.Write(writer, value.Id)
	FfiConverterBoolINSTANCE.Write(writer, value.IsSelected)
	FfiConverterStringINSTANCE.Write(writer, value.RemoteAddr)
	FfiConverterBoolINSTANCE.Write(writer, value.IsIp)
	FfiConverterBoolINSTANCE.Write(writer, value.IsRelay)
	FfiConverterUint64INSTANCE.Write(writer, value.RttMs)
	FfiConverterPathStatsRecordINSTANCE.Write(writer, value.Stats)
}

type FfiDestroyerPathSnapshot struct{}

func (_ FfiDestroyerPathSnapshot) Destroy(value PathSnapshot) {
	value.Destroy()
}

// Flattened headline numbers from `noq::PathStats`.
type PathStatsRecord struct {
	// RTT estimate (ms).
	RttMs uint64
	// UDP datagrams sent on this path.
	UdpTxDatagrams uint64
	// UDP bytes sent on this path.
	UdpTxBytes uint64
	// UDP datagrams received on this path.
	UdpRxDatagrams uint64
	// UDP bytes received on this path.
	UdpRxBytes uint64
	// Current congestion window.
	Cwnd uint64
	// Congestion events on this path.
	CongestionEvents uint64
	// Packets considered lost on this path.
	LostPackets uint64
	// Bytes considered lost on this path.
	LostBytes uint64
	// Largest UDP payload this path currently supports.
	CurrentMtu uint32
}

func (r *PathStatsRecord) Destroy() {
	FfiDestroyerUint64{}.Destroy(r.RttMs)
	FfiDestroyerUint64{}.Destroy(r.UdpTxDatagrams)
	FfiDestroyerUint64{}.Destroy(r.UdpTxBytes)
	FfiDestroyerUint64{}.Destroy(r.UdpRxDatagrams)
	FfiDestroyerUint64{}.Destroy(r.UdpRxBytes)
	FfiDestroyerUint64{}.Destroy(r.Cwnd)
	FfiDestroyerUint64{}.Destroy(r.CongestionEvents)
	FfiDestroyerUint64{}.Destroy(r.LostPackets)
	FfiDestroyerUint64{}.Destroy(r.LostBytes)
	FfiDestroyerUint32{}.Destroy(r.CurrentMtu)
}

type FfiConverterPathStatsRecord struct{}

var FfiConverterPathStatsRecordINSTANCE = FfiConverterPathStatsRecord{}

func (c FfiConverterPathStatsRecord) Lift(rb RustBufferI) PathStatsRecord {
	return LiftFromRustBuffer[PathStatsRecord](c, rb)
}

func (c FfiConverterPathStatsRecord) Read(reader io.Reader) PathStatsRecord {
	return PathStatsRecord{
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint64INSTANCE.Read(reader),
		FfiConverterUint32INSTANCE.Read(reader),
	}
}

func (c FfiConverterPathStatsRecord) Lower(value PathStatsRecord) C.RustBuffer {
	return LowerIntoRustBuffer[PathStatsRecord](c, value)
}

func (c FfiConverterPathStatsRecord) LowerExternal(value PathStatsRecord) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PathStatsRecord](c, value))
}

func (c FfiConverterPathStatsRecord) Write(writer io.Writer, value PathStatsRecord) {
	FfiConverterUint64INSTANCE.Write(writer, value.RttMs)
	FfiConverterUint64INSTANCE.Write(writer, value.UdpTxDatagrams)
	FfiConverterUint64INSTANCE.Write(writer, value.UdpTxBytes)
	FfiConverterUint64INSTANCE.Write(writer, value.UdpRxDatagrams)
	FfiConverterUint64INSTANCE.Write(writer, value.UdpRxBytes)
	FfiConverterUint64INSTANCE.Write(writer, value.Cwnd)
	FfiConverterUint64INSTANCE.Write(writer, value.CongestionEvents)
	FfiConverterUint64INSTANCE.Write(writer, value.LostPackets)
	FfiConverterUint64INSTANCE.Write(writer, value.LostBytes)
	FfiConverterUint32INSTANCE.Write(writer, value.CurrentMtu)
}

type FfiDestroyerPathStatsRecord struct{}

func (_ FfiDestroyerPathStatsRecord) Destroy(value PathStatsRecord) {
	value.Destroy()
}

// Config for a single relay server.
//
// `url` must parse as a `RelayUrl` (HTTPS URL). `quic_port` enables QUIC
// address discovery when set; leaving it `None` disables it. `auth_token`
// becomes an `Authorization: Bearer ...` header on the upgrade request.
type RelayConfig struct {
	Url       string
	QuicPort  *uint16
	AuthToken *string
}

func (r *RelayConfig) Destroy() {
	FfiDestroyerString{}.Destroy(r.Url)
	FfiDestroyerOptionalUint16{}.Destroy(r.QuicPort)
	FfiDestroyerOptionalString{}.Destroy(r.AuthToken)
}

type FfiConverterRelayConfig struct{}

var FfiConverterRelayConfigINSTANCE = FfiConverterRelayConfig{}

func (c FfiConverterRelayConfig) Lift(rb RustBufferI) RelayConfig {
	return LiftFromRustBuffer[RelayConfig](c, rb)
}

func (c FfiConverterRelayConfig) Read(reader io.Reader) RelayConfig {
	return RelayConfig{
		FfiConverterStringINSTANCE.Read(reader),
		FfiConverterOptionalUint16INSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
	}
}

func (c FfiConverterRelayConfig) Lower(value RelayConfig) C.RustBuffer {
	return LowerIntoRustBuffer[RelayConfig](c, value)
}

func (c FfiConverterRelayConfig) LowerExternal(value RelayConfig) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[RelayConfig](c, value))
}

func (c FfiConverterRelayConfig) Write(writer io.Writer, value RelayConfig) {
	FfiConverterStringINSTANCE.Write(writer, value.Url)
	FfiConverterOptionalUint16INSTANCE.Write(writer, value.QuicPort)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.AuthToken)
}

type FfiDestroyerRelayConfig struct{}

func (_ FfiDestroyerRelayConfig) Destroy(value RelayConfig) {
	value.Destroy()
}

// Build options for [`ServicesClient`].
//
// Supply *exactly one* of `api_secret`, `api_secret_from_env`, or
// `ssh_key_pem` for the credential. `api_secret_from_env` (when true) reads
// the `IROH_SERVICES_API_SECRET` environment variable. If a name is provided
// it is registered with the service; the name must be 2–128 UTF-8 bytes.
type ServicesOptions struct {
	// Encoded API secret string (`services1...`). Sets both the remote endpoint
	// to dial and the per-client capability.
	ApiSecret *string
	// If true, read the API secret from `IROH_SERVICES_API_SECRET`.
	ApiSecretFromEnv *bool
	// Unencrypted PEM-encoded OpenSSH ed25519 private key. Grants full
	// capabilities; used by node operators / project owners.
	SshKeyPem *string
	// Optional endpoint name to register cloud-side.
	Name *string
	// How often (in milliseconds) to push metrics to the service. `0` disables
	// automatic interval pushes; if omitted the upstream default applies.
	MetricsIntervalMs *uint64
}

func (r *ServicesOptions) Destroy() {
	FfiDestroyerOptionalString{}.Destroy(r.ApiSecret)
	FfiDestroyerOptionalBool{}.Destroy(r.ApiSecretFromEnv)
	FfiDestroyerOptionalString{}.Destroy(r.SshKeyPem)
	FfiDestroyerOptionalString{}.Destroy(r.Name)
	FfiDestroyerOptionalUint64{}.Destroy(r.MetricsIntervalMs)
}

type FfiConverterServicesOptions struct{}

var FfiConverterServicesOptionsINSTANCE = FfiConverterServicesOptions{}

func (c FfiConverterServicesOptions) Lift(rb RustBufferI) ServicesOptions {
	return LiftFromRustBuffer[ServicesOptions](c, rb)
}

func (c FfiConverterServicesOptions) Read(reader io.Reader) ServicesOptions {
	return ServicesOptions{
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalBoolINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalStringINSTANCE.Read(reader),
		FfiConverterOptionalUint64INSTANCE.Read(reader),
	}
}

func (c FfiConverterServicesOptions) Lower(value ServicesOptions) C.RustBuffer {
	return LowerIntoRustBuffer[ServicesOptions](c, value)
}

func (c FfiConverterServicesOptions) LowerExternal(value ServicesOptions) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[ServicesOptions](c, value))
}

func (c FfiConverterServicesOptions) Write(writer io.Writer, value ServicesOptions) {
	FfiConverterOptionalStringINSTANCE.Write(writer, value.ApiSecret)
	FfiConverterOptionalBoolINSTANCE.Write(writer, value.ApiSecretFromEnv)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.SshKeyPem)
	FfiConverterOptionalStringINSTANCE.Write(writer, value.Name)
	FfiConverterOptionalUint64INSTANCE.Write(writer, value.MetricsIntervalMs)
}

type FfiDestroyerServicesOptions struct{}

func (_ FfiDestroyerServicesOptions) Destroy(value ServicesOptions) {
	value.Destroy()
}

type CallbackError struct {
	err error
}

// Convenience method to turn *CallbackError into error
// Avoiding treating nil pointer as non nil error interface
func (err *CallbackError) AsError() error {
	if err == nil {
		return nil
	} else {
		return err
	}
}

func (err CallbackError) Error() string {
	return fmt.Sprintf("CallbackError: %s", err.err.Error())
}

func (err CallbackError) Unwrap() error {
	return err.err
}

// Err* are used for checking error type with `errors.Is`
var ErrCallbackErrorError = fmt.Errorf("CallbackErrorError")

// Variant structs
type CallbackErrorError struct {
}

func NewCallbackErrorError() *CallbackError {
	return &CallbackError{err: &CallbackErrorError{}}
}

func (e CallbackErrorError) destroy() {
}

func (err CallbackErrorError) Error() string {
	return fmt.Sprint("Error")
}

func (self CallbackErrorError) Is(target error) bool {
	return target == ErrCallbackErrorError
}

type FfiConverterCallbackError struct{}

var FfiConverterCallbackErrorINSTANCE = FfiConverterCallbackError{}

func (c FfiConverterCallbackError) Lift(eb RustBufferI) *CallbackError {
	return LiftFromRustBuffer[*CallbackError](c, eb)
}

func (c FfiConverterCallbackError) Lower(value *CallbackError) C.RustBuffer {
	return LowerIntoRustBuffer[*CallbackError](c, value)
}

func (c FfiConverterCallbackError) LowerExternal(value *CallbackError) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*CallbackError](c, value))
}

func (c FfiConverterCallbackError) Read(reader io.Reader) *CallbackError {
	errorID := readUint32(reader)

	switch errorID {
	case 1:
		return &CallbackError{&CallbackErrorError{}}
	default:
		panic(fmt.Sprintf("Unknown error code %d in FfiConverterCallbackError.Read()", errorID))
	}
}

func (c FfiConverterCallbackError) Write(writer io.Writer, value *CallbackError) {
	switch variantValue := value.err.(type) {
	case *CallbackErrorError:
		writeInt32(writer, 1)
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiConverterCallbackError.Write", value))
	}
}

type FfiDestroyerCallbackError struct{}

func (_ FfiDestroyerCallbackError) Destroy(value *CallbackError) {
	switch variantValue := value.err.(type) {
	case CallbackErrorError:
		variantValue.destroy()
	default:
		_ = variantValue
		panic(fmt.Sprintf("invalid error value `%v` in FfiDestroyerCallbackError.Destroy", value))
	}
}

// Where an incoming connection came from.
type IncomingAddr interface {
	Destroy()
}

// A direct connection from an IP address (`ip:port` string).
type IncomingAddrIp struct {
	Addr string
}

func (e IncomingAddrIp) Destroy() {
	FfiDestroyerString{}.Destroy(e.Addr)
}

// A connection via a relay.
type IncomingAddrRelay struct {
	Url        string
	EndpointId *EndpointId
}

func (e IncomingAddrRelay) Destroy() {
	FfiDestroyerString{}.Destroy(e.Url)
	FfiDestroyerEndpointId{}.Destroy(e.EndpointId)
}

// A custom-transport connection (rendered as its debug form).
type IncomingAddrCustom struct {
	Description string
}

func (e IncomingAddrCustom) Destroy() {
	FfiDestroyerString{}.Destroy(e.Description)
}

type FfiConverterIncomingAddr struct{}

var FfiConverterIncomingAddrINSTANCE = FfiConverterIncomingAddr{}

func (c FfiConverterIncomingAddr) Lift(rb RustBufferI) IncomingAddr {
	return LiftFromRustBuffer[IncomingAddr](c, rb)
}

func (c FfiConverterIncomingAddr) Lower(value IncomingAddr) C.RustBuffer {
	return LowerIntoRustBuffer[IncomingAddr](c, value)
}

func (c FfiConverterIncomingAddr) LowerExternal(value IncomingAddr) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[IncomingAddr](c, value))
}
func (FfiConverterIncomingAddr) Read(reader io.Reader) IncomingAddr {
	id := readInt32(reader)
	switch id {
	case 1:
		return IncomingAddrIp{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 2:
		return IncomingAddrRelay{
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterEndpointIdINSTANCE.Read(reader),
		}
	case 3:
		return IncomingAddrCustom{
			FfiConverterStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterIncomingAddr.Read()", id))
	}
}

func (FfiConverterIncomingAddr) Write(writer io.Writer, value IncomingAddr) {
	switch variant_value := value.(type) {
	case IncomingAddrIp:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Addr)
	case IncomingAddrRelay:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Url)
		FfiConverterEndpointIdINSTANCE.Write(writer, variant_value.EndpointId)
	case IncomingAddrCustom:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Description)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterIncomingAddr.Write", value))
	}
}

type FfiDestroyerIncomingAddr struct{}

func (_ FfiDestroyerIncomingAddr) Destroy(value IncomingAddr) {
	value.Destroy()
}

// The local address that received an incoming connection.
type IncomingLocalAddr interface {
	Destroy()
}

// Direct IP (`ip` string if available).
type IncomingLocalAddrIp struct {
	Addr *string
}

func (e IncomingLocalAddrIp) Destroy() {
	FfiDestroyerOptionalString{}.Destroy(e.Addr)
}

// Relay path.
type IncomingLocalAddrRelay struct {
	Url string
}

func (e IncomingLocalAddrRelay) Destroy() {
	FfiDestroyerString{}.Destroy(e.Url)
}

// Custom transport.
type IncomingLocalAddrCustom struct {
	Description *string
}

func (e IncomingLocalAddrCustom) Destroy() {
	FfiDestroyerOptionalString{}.Destroy(e.Description)
}

type FfiConverterIncomingLocalAddr struct{}

var FfiConverterIncomingLocalAddrINSTANCE = FfiConverterIncomingLocalAddr{}

func (c FfiConverterIncomingLocalAddr) Lift(rb RustBufferI) IncomingLocalAddr {
	return LiftFromRustBuffer[IncomingLocalAddr](c, rb)
}

func (c FfiConverterIncomingLocalAddr) Lower(value IncomingLocalAddr) C.RustBuffer {
	return LowerIntoRustBuffer[IncomingLocalAddr](c, value)
}

func (c FfiConverterIncomingLocalAddr) LowerExternal(value IncomingLocalAddr) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[IncomingLocalAddr](c, value))
}
func (FfiConverterIncomingLocalAddr) Read(reader io.Reader) IncomingLocalAddr {
	id := readInt32(reader)
	switch id {
	case 1:
		return IncomingLocalAddrIp{
			FfiConverterOptionalStringINSTANCE.Read(reader),
		}
	case 2:
		return IncomingLocalAddrRelay{
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 3:
		return IncomingLocalAddrCustom{
			FfiConverterOptionalStringINSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterIncomingLocalAddr.Read()", id))
	}
}

func (FfiConverterIncomingLocalAddr) Write(writer io.Writer, value IncomingLocalAddr) {
	switch variant_value := value.(type) {
	case IncomingLocalAddrIp:
		writeInt32(writer, 1)
		FfiConverterOptionalStringINSTANCE.Write(writer, variant_value.Addr)
	case IncomingLocalAddrRelay:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Url)
	case IncomingLocalAddrCustom:
		writeInt32(writer, 3)
		FfiConverterOptionalStringINSTANCE.Write(writer, variant_value.Description)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterIncomingLocalAddr.Write", value))
	}
}

type FfiDestroyerIncomingLocalAddr struct{}

func (_ FfiDestroyerIncomingLocalAddr) Destroy(value IncomingLocalAddr) {
	value.Destroy()
}

// Stable high-level error categories exposed across the FFI boundary.
//
// These are intentionally coarser than the upstream Rust error types. They
// give foreign bindings a stable taxonomy for `errors.Is`-style handling
// without leaking the internal `iroh` / `n0-error` error hierarchy.
type IrohErrorKind uint

const (
	// Invalid input supplied by the caller.
	IrohErrorKindInvalidInput IrohErrorKind = 1
	// Failure while binding an endpoint.
	IrohErrorKindBind IrohErrorKind = 2
	// Failure while initiating or completing an outgoing connection.
	IrohErrorKindConnect IrohErrorKind = 3
	// An established connection failed or closed unexpectedly.
	IrohErrorKindConnection IrohErrorKind = 4
	// ALPN negotiation or lookup failed.
	IrohErrorKindAlpn IrohErrorKind = 5
	// Endpoint id / public key parsing failed.
	IrohErrorKindKeyParsing IrohErrorKind = 6
	// Ticket parsing failed.
	IrohErrorKindTicketParsing IrohErrorKind = 7
	// Relay configuration or relay operation failed.
	IrohErrorKindRelay IrohErrorKind = 8
	// Stream read/write/control operation failed.
	IrohErrorKindStream IrohErrorKind = 9
	// Datagram send/receive operation failed.
	IrohErrorKindDatagram IrohErrorKind = 10
	// Foreign callback failed.
	IrohErrorKindCallback IrohErrorKind = 11
	// Operation was attempted on a closed stream/connection/resource.
	IrohErrorKindClosed IrohErrorKind = 12
	// Operation timed out.
	IrohErrorKindTimeout IrohErrorKind = 13
	// Unclassified internal error.
	IrohErrorKindInternal IrohErrorKind = 14
)

type FfiConverterIrohErrorKind struct{}

var FfiConverterIrohErrorKindINSTANCE = FfiConverterIrohErrorKind{}

func (c FfiConverterIrohErrorKind) Lift(rb RustBufferI) IrohErrorKind {
	return LiftFromRustBuffer[IrohErrorKind](c, rb)
}

func (c FfiConverterIrohErrorKind) Lower(value IrohErrorKind) C.RustBuffer {
	return LowerIntoRustBuffer[IrohErrorKind](c, value)
}

func (c FfiConverterIrohErrorKind) LowerExternal(value IrohErrorKind) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[IrohErrorKind](c, value))
}
func (FfiConverterIrohErrorKind) Read(reader io.Reader) IrohErrorKind {
	id := readInt32(reader)
	return IrohErrorKind(id)
}

func (FfiConverterIrohErrorKind) Write(writer io.Writer, value IrohErrorKind) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerIrohErrorKind struct{}

func (_ FfiDestroyerIrohErrorKind) Destroy(value IrohErrorKind) {
}

// The logging level. See the rust (log crate)[https://docs.rs/log] for more information.
type LogLevel uint

const (
	LogLevelTrace LogLevel = 1
	LogLevelDebug LogLevel = 2
	LogLevelInfo  LogLevel = 3
	LogLevelWarn  LogLevel = 4
	LogLevelError LogLevel = 5
	LogLevelOff   LogLevel = 6
)

type FfiConverterLogLevel struct{}

var FfiConverterLogLevelINSTANCE = FfiConverterLogLevel{}

func (c FfiConverterLogLevel) Lift(rb RustBufferI) LogLevel {
	return LiftFromRustBuffer[LogLevel](c, rb)
}

func (c FfiConverterLogLevel) Lower(value LogLevel) C.RustBuffer {
	return LowerIntoRustBuffer[LogLevel](c, value)
}

func (c FfiConverterLogLevel) LowerExternal(value LogLevel) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[LogLevel](c, value))
}
func (FfiConverterLogLevel) Read(reader io.Reader) LogLevel {
	id := readInt32(reader)
	return LogLevel(id)
}

func (FfiConverterLogLevel) Write(writer io.Writer, value LogLevel) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerLogLevel struct{}

func (_ FfiDestroyerLogLevel) Destroy(value LogLevel) {
}

// An event from `Connection::path_events`.
type PathEvent interface {
	Destroy()
}

// A new network path was opened.
type PathEventOpened struct {
	Id         string
	RemoteAddr string
	LocalAddr  string
}

func (e PathEventOpened) Destroy() {
	FfiDestroyerString{}.Destroy(e.Id)
	FfiDestroyerString{}.Destroy(e.RemoteAddr)
	FfiDestroyerString{}.Destroy(e.LocalAddr)
}

// A network path was closed.
type PathEventClosed struct {
	Id         string
	RemoteAddr string
	LocalAddr  string
	LastStats  PathStatsRecord
}

func (e PathEventClosed) Destroy() {
	FfiDestroyerString{}.Destroy(e.Id)
	FfiDestroyerString{}.Destroy(e.RemoteAddr)
	FfiDestroyerString{}.Destroy(e.LocalAddr)
	FfiDestroyerPathStatsRecord{}.Destroy(e.LastStats)
}

// This path was selected for transmission of application data.
type PathEventSelected struct {
	Id         string
	RemoteAddr string
	LocalAddr  string
}

func (e PathEventSelected) Destroy() {
	FfiDestroyerString{}.Destroy(e.Id)
	FfiDestroyerString{}.Destroy(e.RemoteAddr)
	FfiDestroyerString{}.Destroy(e.LocalAddr)
}

// Events were dropped before the subscriber received them.
type PathEventLagged struct {
	Missed uint64
}

func (e PathEventLagged) Destroy() {
	FfiDestroyerUint64{}.Destroy(e.Missed)
}

type FfiConverterPathEvent struct{}

var FfiConverterPathEventINSTANCE = FfiConverterPathEvent{}

func (c FfiConverterPathEvent) Lift(rb RustBufferI) PathEvent {
	return LiftFromRustBuffer[PathEvent](c, rb)
}

func (c FfiConverterPathEvent) Lower(value PathEvent) C.RustBuffer {
	return LowerIntoRustBuffer[PathEvent](c, value)
}

func (c FfiConverterPathEvent) LowerExternal(value PathEvent) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[PathEvent](c, value))
}
func (FfiConverterPathEvent) Read(reader io.Reader) PathEvent {
	id := readInt32(reader)
	switch id {
	case 1:
		return PathEventOpened{
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 2:
		return PathEventClosed{
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterPathStatsRecordINSTANCE.Read(reader),
		}
	case 3:
		return PathEventSelected{
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
			FfiConverterStringINSTANCE.Read(reader),
		}
	case 4:
		return PathEventLagged{
			FfiConverterUint64INSTANCE.Read(reader),
		}
	default:
		panic(fmt.Sprintf("invalid enum value %v in FfiConverterPathEvent.Read()", id))
	}
}

func (FfiConverterPathEvent) Write(writer io.Writer, value PathEvent) {
	switch variant_value := value.(type) {
	case PathEventOpened:
		writeInt32(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Id)
		FfiConverterStringINSTANCE.Write(writer, variant_value.RemoteAddr)
		FfiConverterStringINSTANCE.Write(writer, variant_value.LocalAddr)
	case PathEventClosed:
		writeInt32(writer, 2)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Id)
		FfiConverterStringINSTANCE.Write(writer, variant_value.RemoteAddr)
		FfiConverterStringINSTANCE.Write(writer, variant_value.LocalAddr)
		FfiConverterPathStatsRecordINSTANCE.Write(writer, variant_value.LastStats)
	case PathEventSelected:
		writeInt32(writer, 3)
		FfiConverterStringINSTANCE.Write(writer, variant_value.Id)
		FfiConverterStringINSTANCE.Write(writer, variant_value.RemoteAddr)
		FfiConverterStringINSTANCE.Write(writer, variant_value.LocalAddr)
	case PathEventLagged:
		writeInt32(writer, 4)
		FfiConverterUint64INSTANCE.Write(writer, variant_value.Missed)
	default:
		_ = variant_value
		panic(fmt.Sprintf("invalid enum value `%v` in FfiConverterPathEvent.Write", value))
	}
}

type FfiDestroyerPathEvent struct{}

func (_ FfiDestroyerPathEvent) Destroy(value PathEvent) {
	value.Destroy()
}

// Which side of a connection we are.
type Side uint

const (
	// We initiated this connection.
	SideClient Side = 1
	// We accepted this connection.
	SideServer Side = 2
)

type FfiConverterSide struct{}

var FfiConverterSideINSTANCE = FfiConverterSide{}

func (c FfiConverterSide) Lift(rb RustBufferI) Side {
	return LiftFromRustBuffer[Side](c, rb)
}

func (c FfiConverterSide) Lower(value Side) C.RustBuffer {
	return LowerIntoRustBuffer[Side](c, value)
}

func (c FfiConverterSide) LowerExternal(value Side) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[Side](c, value))
}
func (FfiConverterSide) Read(reader io.Reader) Side {
	id := readInt32(reader)
	return Side(id)
}

func (FfiConverterSide) Write(writer io.Writer, value Side) {
	writeInt32(writer, int32(value))
}

type FfiDestroyerSide struct{}

func (_ FfiDestroyerSide) Destroy(value Side) {
}

type FfiConverterOptionalUint16 struct{}

var FfiConverterOptionalUint16INSTANCE = FfiConverterOptionalUint16{}

func (c FfiConverterOptionalUint16) Lift(rb RustBufferI) *uint16 {
	return LiftFromRustBuffer[*uint16](c, rb)
}

func (_ FfiConverterOptionalUint16) Read(reader io.Reader) *uint16 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterUint16INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalUint16) Lower(value *uint16) C.RustBuffer {
	return LowerIntoRustBuffer[*uint16](c, value)
}

func (c FfiConverterOptionalUint16) LowerExternal(value *uint16) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*uint16](c, value))
}

func (_ FfiConverterOptionalUint16) Write(writer io.Writer, value *uint16) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterUint16INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalUint16 struct{}

func (_ FfiDestroyerOptionalUint16) Destroy(value *uint16) {
	if value != nil {
		FfiDestroyerUint16{}.Destroy(*value)
	}
}

type FfiConverterOptionalUint64 struct{}

var FfiConverterOptionalUint64INSTANCE = FfiConverterOptionalUint64{}

func (c FfiConverterOptionalUint64) Lift(rb RustBufferI) *uint64 {
	return LiftFromRustBuffer[*uint64](c, rb)
}

func (_ FfiConverterOptionalUint64) Read(reader io.Reader) *uint64 {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterUint64INSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalUint64) Lower(value *uint64) C.RustBuffer {
	return LowerIntoRustBuffer[*uint64](c, value)
}

func (c FfiConverterOptionalUint64) LowerExternal(value *uint64) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*uint64](c, value))
}

func (_ FfiConverterOptionalUint64) Write(writer io.Writer, value *uint64) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterUint64INSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalUint64 struct{}

func (_ FfiDestroyerOptionalUint64) Destroy(value *uint64) {
	if value != nil {
		FfiDestroyerUint64{}.Destroy(*value)
	}
}

type FfiConverterOptionalBool struct{}

var FfiConverterOptionalBoolINSTANCE = FfiConverterOptionalBool{}

func (c FfiConverterOptionalBool) Lift(rb RustBufferI) *bool {
	return LiftFromRustBuffer[*bool](c, rb)
}

func (_ FfiConverterOptionalBool) Read(reader io.Reader) *bool {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterBoolINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalBool) Lower(value *bool) C.RustBuffer {
	return LowerIntoRustBuffer[*bool](c, value)
}

func (c FfiConverterOptionalBool) LowerExternal(value *bool) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*bool](c, value))
}

func (_ FfiConverterOptionalBool) Write(writer io.Writer, value *bool) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterBoolINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalBool struct{}

func (_ FfiDestroyerOptionalBool) Destroy(value *bool) {
	if value != nil {
		FfiDestroyerBool{}.Destroy(*value)
	}
}

type FfiConverterOptionalString struct{}

var FfiConverterOptionalStringINSTANCE = FfiConverterOptionalString{}

func (c FfiConverterOptionalString) Lift(rb RustBufferI) *string {
	return LiftFromRustBuffer[*string](c, rb)
}

func (_ FfiConverterOptionalString) Read(reader io.Reader) *string {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterStringINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalString) Lower(value *string) C.RustBuffer {
	return LowerIntoRustBuffer[*string](c, value)
}

func (c FfiConverterOptionalString) LowerExternal(value *string) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*string](c, value))
}

func (_ FfiConverterOptionalString) Write(writer io.Writer, value *string) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterStringINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalString struct{}

func (_ FfiDestroyerOptionalString) Destroy(value *string) {
	if value != nil {
		FfiDestroyerString{}.Destroy(*value)
	}
}

type FfiConverterOptionalBytes struct{}

var FfiConverterOptionalBytesINSTANCE = FfiConverterOptionalBytes{}

func (c FfiConverterOptionalBytes) Lift(rb RustBufferI) *[]byte {
	return LiftFromRustBuffer[*[]byte](c, rb)
}

func (_ FfiConverterOptionalBytes) Read(reader io.Reader) *[]byte {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterBytesINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalBytes) Lower(value *[]byte) C.RustBuffer {
	return LowerIntoRustBuffer[*[]byte](c, value)
}

func (c FfiConverterOptionalBytes) LowerExternal(value *[]byte) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*[]byte](c, value))
}

func (_ FfiConverterOptionalBytes) Write(writer io.Writer, value *[]byte) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterBytesINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalBytes struct{}

func (_ FfiDestroyerOptionalBytes) Destroy(value *[]byte) {
	if value != nil {
		FfiDestroyerBytes{}.Destroy(*value)
	}
}

type FfiConverterOptionalEndpointAddr struct{}

var FfiConverterOptionalEndpointAddrINSTANCE = FfiConverterOptionalEndpointAddr{}

func (c FfiConverterOptionalEndpointAddr) Lift(rb RustBufferI) **EndpointAddr {
	return LiftFromRustBuffer[**EndpointAddr](c, rb)
}

func (_ FfiConverterOptionalEndpointAddr) Read(reader io.Reader) **EndpointAddr {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterEndpointAddrINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalEndpointAddr) Lower(value **EndpointAddr) C.RustBuffer {
	return LowerIntoRustBuffer[**EndpointAddr](c, value)
}

func (c FfiConverterOptionalEndpointAddr) LowerExternal(value **EndpointAddr) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[**EndpointAddr](c, value))
}

func (_ FfiConverterOptionalEndpointAddr) Write(writer io.Writer, value **EndpointAddr) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterEndpointAddrINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalEndpointAddr struct{}

func (_ FfiDestroyerOptionalEndpointAddr) Destroy(value **EndpointAddr) {
	if value != nil {
		FfiDestroyerEndpointAddr{}.Destroy(*value)
	}
}

type FfiConverterOptionalIncoming struct{}

var FfiConverterOptionalIncomingINSTANCE = FfiConverterOptionalIncoming{}

func (c FfiConverterOptionalIncoming) Lift(rb RustBufferI) **Incoming {
	return LiftFromRustBuffer[**Incoming](c, rb)
}

func (_ FfiConverterOptionalIncoming) Read(reader io.Reader) **Incoming {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterIncomingINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalIncoming) Lower(value **Incoming) C.RustBuffer {
	return LowerIntoRustBuffer[**Incoming](c, value)
}

func (c FfiConverterOptionalIncoming) LowerExternal(value **Incoming) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[**Incoming](c, value))
}

func (_ FfiConverterOptionalIncoming) Write(writer io.Writer, value **Incoming) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterIncomingINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalIncoming struct{}

func (_ FfiDestroyerOptionalIncoming) Destroy(value **Incoming) {
	if value != nil {
		FfiDestroyerIncoming{}.Destroy(*value)
	}
}

type FfiConverterOptionalPreset struct{}

var FfiConverterOptionalPresetINSTANCE = FfiConverterOptionalPreset{}

func (c FfiConverterOptionalPreset) Lift(rb RustBufferI) *Preset {
	return LiftFromRustBuffer[*Preset](c, rb)
}

func (_ FfiConverterOptionalPreset) Read(reader io.Reader) *Preset {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterPresetINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalPreset) Lower(value *Preset) C.RustBuffer {
	return LowerIntoRustBuffer[*Preset](c, value)
}

func (c FfiConverterOptionalPreset) LowerExternal(value *Preset) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*Preset](c, value))
}

func (_ FfiConverterOptionalPreset) Write(writer io.Writer, value *Preset) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterPresetINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalPreset struct{}

func (_ FfiDestroyerOptionalPreset) Destroy(value *Preset) {
	if value != nil {
		FfiDestroyerPreset{}.Destroy(*value)
	}
}

type FfiConverterOptionalRelayMode struct{}

var FfiConverterOptionalRelayModeINSTANCE = FfiConverterOptionalRelayMode{}

func (c FfiConverterOptionalRelayMode) Lift(rb RustBufferI) **RelayMode {
	return LiftFromRustBuffer[**RelayMode](c, rb)
}

func (_ FfiConverterOptionalRelayMode) Read(reader io.Reader) **RelayMode {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterRelayModeINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalRelayMode) Lower(value **RelayMode) C.RustBuffer {
	return LowerIntoRustBuffer[**RelayMode](c, value)
}

func (c FfiConverterOptionalRelayMode) LowerExternal(value **RelayMode) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[**RelayMode](c, value))
}

func (_ FfiConverterOptionalRelayMode) Write(writer io.Writer, value **RelayMode) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterRelayModeINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalRelayMode struct{}

func (_ FfiDestroyerOptionalRelayMode) Destroy(value **RelayMode) {
	if value != nil {
		FfiDestroyerRelayMode{}.Destroy(*value)
	}
}

type FfiConverterOptionalRelayConfig struct{}

var FfiConverterOptionalRelayConfigINSTANCE = FfiConverterOptionalRelayConfig{}

func (c FfiConverterOptionalRelayConfig) Lift(rb RustBufferI) *RelayConfig {
	return LiftFromRustBuffer[*RelayConfig](c, rb)
}

func (_ FfiConverterOptionalRelayConfig) Read(reader io.Reader) *RelayConfig {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterRelayConfigINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalRelayConfig) Lower(value *RelayConfig) C.RustBuffer {
	return LowerIntoRustBuffer[*RelayConfig](c, value)
}

func (c FfiConverterOptionalRelayConfig) LowerExternal(value *RelayConfig) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*RelayConfig](c, value))
}

func (_ FfiConverterOptionalRelayConfig) Write(writer io.Writer, value *RelayConfig) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterRelayConfigINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalRelayConfig struct{}

func (_ FfiDestroyerOptionalRelayConfig) Destroy(value *RelayConfig) {
	if value != nil {
		FfiDestroyerRelayConfig{}.Destroy(*value)
	}
}

type FfiConverterOptionalSequenceBytes struct{}

var FfiConverterOptionalSequenceBytesINSTANCE = FfiConverterOptionalSequenceBytes{}

func (c FfiConverterOptionalSequenceBytes) Lift(rb RustBufferI) *[][]byte {
	return LiftFromRustBuffer[*[][]byte](c, rb)
}

func (_ FfiConverterOptionalSequenceBytes) Read(reader io.Reader) *[][]byte {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterSequenceBytesINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalSequenceBytes) Lower(value *[][]byte) C.RustBuffer {
	return LowerIntoRustBuffer[*[][]byte](c, value)
}

func (c FfiConverterOptionalSequenceBytes) LowerExternal(value *[][]byte) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*[][]byte](c, value))
}

func (_ FfiConverterOptionalSequenceBytes) Write(writer io.Writer, value *[][]byte) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterSequenceBytesINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalSequenceBytes struct{}

func (_ FfiDestroyerOptionalSequenceBytes) Destroy(value *[][]byte) {
	if value != nil {
		FfiDestroyerSequenceBytes{}.Destroy(*value)
	}
}

type FfiConverterOptionalMapBytesProtocolCreator struct{}

var FfiConverterOptionalMapBytesProtocolCreatorINSTANCE = FfiConverterOptionalMapBytesProtocolCreator{}

func (c FfiConverterOptionalMapBytesProtocolCreator) Lift(rb RustBufferI) *map[string]ProtocolCreator {
	return LiftFromRustBuffer[*map[string]ProtocolCreator](c, rb)
}

func (_ FfiConverterOptionalMapBytesProtocolCreator) Read(reader io.Reader) *map[string]ProtocolCreator {
	if readInt8(reader) == 0 {
		return nil
	}
	temp := FfiConverterMapBytesProtocolCreatorINSTANCE.Read(reader)
	return &temp
}

func (c FfiConverterOptionalMapBytesProtocolCreator) Lower(value *map[string]ProtocolCreator) C.RustBuffer {
	return LowerIntoRustBuffer[*map[string]ProtocolCreator](c, value)
}

func (c FfiConverterOptionalMapBytesProtocolCreator) LowerExternal(value *map[string]ProtocolCreator) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[*map[string]ProtocolCreator](c, value))
}

func (_ FfiConverterOptionalMapBytesProtocolCreator) Write(writer io.Writer, value *map[string]ProtocolCreator) {
	if value == nil {
		writeInt8(writer, 0)
	} else {
		writeInt8(writer, 1)
		FfiConverterMapBytesProtocolCreatorINSTANCE.Write(writer, *value)
	}
}

type FfiDestroyerOptionalMapBytesProtocolCreator struct{}

func (_ FfiDestroyerOptionalMapBytesProtocolCreator) Destroy(value *map[string]ProtocolCreator) {
	if value != nil {
		FfiDestroyerMapBytesProtocolCreator{}.Destroy(*value)
	}
}

type FfiConverterSequenceString struct{}

var FfiConverterSequenceStringINSTANCE = FfiConverterSequenceString{}

func (c FfiConverterSequenceString) Lift(rb RustBufferI) []string {
	return LiftFromRustBuffer[[]string](c, rb)
}

func (c FfiConverterSequenceString) Read(reader io.Reader) []string {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]string, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterStringINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceString) Lower(value []string) C.RustBuffer {
	return LowerIntoRustBuffer[[]string](c, value)
}

func (c FfiConverterSequenceString) LowerExternal(value []string) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]string](c, value))
}

func (c FfiConverterSequenceString) Write(writer io.Writer, value []string) {
	if len(value) > math.MaxInt32 {
		panic("[]string is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterStringINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceString struct{}

func (FfiDestroyerSequenceString) Destroy(sequence []string) {
	for _, value := range sequence {
		FfiDestroyerString{}.Destroy(value)
	}
}

type FfiConverterSequenceBytes struct{}

var FfiConverterSequenceBytesINSTANCE = FfiConverterSequenceBytes{}

func (c FfiConverterSequenceBytes) Lift(rb RustBufferI) [][]byte {
	return LiftFromRustBuffer[[][]byte](c, rb)
}

func (c FfiConverterSequenceBytes) Read(reader io.Reader) [][]byte {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([][]byte, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterBytesINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequenceBytes) Lower(value [][]byte) C.RustBuffer {
	return LowerIntoRustBuffer[[][]byte](c, value)
}

func (c FfiConverterSequenceBytes) LowerExternal(value [][]byte) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[][]byte](c, value))
}

func (c FfiConverterSequenceBytes) Write(writer io.Writer, value [][]byte) {
	if len(value) > math.MaxInt32 {
		panic("[][]byte is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterBytesINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequenceBytes struct{}

func (FfiDestroyerSequenceBytes) Destroy(sequence [][]byte) {
	for _, value := range sequence {
		FfiDestroyerBytes{}.Destroy(value)
	}
}

type FfiConverterSequencePathSnapshot struct{}

var FfiConverterSequencePathSnapshotINSTANCE = FfiConverterSequencePathSnapshot{}

func (c FfiConverterSequencePathSnapshot) Lift(rb RustBufferI) []PathSnapshot {
	return LiftFromRustBuffer[[]PathSnapshot](c, rb)
}

func (c FfiConverterSequencePathSnapshot) Read(reader io.Reader) []PathSnapshot {
	length := readInt32(reader)
	if length == 0 {
		return nil
	}
	result := make([]PathSnapshot, 0, length)
	for i := int32(0); i < length; i++ {
		result = append(result, FfiConverterPathSnapshotINSTANCE.Read(reader))
	}
	return result
}

func (c FfiConverterSequencePathSnapshot) Lower(value []PathSnapshot) C.RustBuffer {
	return LowerIntoRustBuffer[[]PathSnapshot](c, value)
}

func (c FfiConverterSequencePathSnapshot) LowerExternal(value []PathSnapshot) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[[]PathSnapshot](c, value))
}

func (c FfiConverterSequencePathSnapshot) Write(writer io.Writer, value []PathSnapshot) {
	if len(value) > math.MaxInt32 {
		panic("[]PathSnapshot is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(value)))
	for _, item := range value {
		FfiConverterPathSnapshotINSTANCE.Write(writer, item)
	}
}

type FfiDestroyerSequencePathSnapshot struct{}

func (FfiDestroyerSequencePathSnapshot) Destroy(sequence []PathSnapshot) {
	for _, value := range sequence {
		FfiDestroyerPathSnapshot{}.Destroy(value)
	}
}

type FfiConverterMapStringCounterStats struct{}

var FfiConverterMapStringCounterStatsINSTANCE = FfiConverterMapStringCounterStats{}

func (c FfiConverterMapStringCounterStats) Lift(rb RustBufferI) map[string]CounterStats {
	return LiftFromRustBuffer[map[string]CounterStats](c, rb)
}

func (_ FfiConverterMapStringCounterStats) Read(reader io.Reader) map[string]CounterStats {
	result := make(map[string]CounterStats)
	length := readInt32(reader)
	for i := int32(0); i < length; i++ {
		key := FfiConverterStringINSTANCE.Read(reader)
		value := FfiConverterCounterStatsINSTANCE.Read(reader)
		result[string(key)] = value
	}
	return result
}

func (c FfiConverterMapStringCounterStats) Lower(value map[string]CounterStats) C.RustBuffer {
	return LowerIntoRustBuffer[map[string]CounterStats](c, value)
}

func (c FfiConverterMapStringCounterStats) LowerExternal(value map[string]CounterStats) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[map[string]CounterStats](c, value))
}

func (_ FfiConverterMapStringCounterStats) Write(writer io.Writer, mapValue map[string]CounterStats) {
	if len(mapValue) > math.MaxInt32 {
		panic("map[string]CounterStats is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(mapValue)))
	for key, value := range mapValue {
		FfiConverterStringINSTANCE.Write(writer, key)
		FfiConverterCounterStatsINSTANCE.Write(writer, value)
	}
}

type FfiDestroyerMapStringCounterStats struct{}

func (_ FfiDestroyerMapStringCounterStats) Destroy(mapValue map[string]CounterStats) {
	for key, value := range mapValue {
		FfiDestroyerString{}.Destroy(key)
		FfiDestroyerCounterStats{}.Destroy(value)
	}
}

type FfiConverterMapBytesProtocolCreator struct{}

var FfiConverterMapBytesProtocolCreatorINSTANCE = FfiConverterMapBytesProtocolCreator{}

func (c FfiConverterMapBytesProtocolCreator) Lift(rb RustBufferI) map[string]ProtocolCreator {
	return LiftFromRustBuffer[map[string]ProtocolCreator](c, rb)
}

func (_ FfiConverterMapBytesProtocolCreator) Read(reader io.Reader) map[string]ProtocolCreator {
	result := make(map[string]ProtocolCreator)
	length := readInt32(reader)
	for i := int32(0); i < length; i++ {
		key := FfiConverterBytesINSTANCE.Read(reader)
		value := FfiConverterProtocolCreatorINSTANCE.Read(reader)
		result[string(key)] = value
	}
	return result
}

func (c FfiConverterMapBytesProtocolCreator) Lower(value map[string]ProtocolCreator) C.RustBuffer {
	return LowerIntoRustBuffer[map[string]ProtocolCreator](c, value)
}

func (c FfiConverterMapBytesProtocolCreator) LowerExternal(value map[string]ProtocolCreator) ExternalCRustBuffer {
	return RustBufferFromC(LowerIntoRustBuffer[map[string]ProtocolCreator](c, value))
}

func (_ FfiConverterMapBytesProtocolCreator) Write(writer io.Writer, mapValue map[string]ProtocolCreator) {
	if len(mapValue) > math.MaxInt32 {
		panic("map[string]ProtocolCreator is too large to fit into Int32")
	}

	writeInt32(writer, int32(len(mapValue)))
	for key, value := range mapValue {
		FfiConverterBytesINSTANCE.Write(writer, []byte(key))
		FfiConverterProtocolCreatorINSTANCE.Write(writer, value)
	}
}

type FfiDestroyerMapBytesProtocolCreator struct{}

func (_ FfiDestroyerMapBytesProtocolCreator) Destroy(mapValue map[string]ProtocolCreator) {
	for key, value := range mapValue {
		FfiDestroyerBytes{}.Destroy([]byte(key))
		FfiDestroyerProtocolCreator{}.Destroy(value)
	}
}

const (
	uniffiRustFuturePollReady      int8 = 0
	uniffiRustFuturePollMaybeReady int8 = 1
)

type rustFuturePollFunc func(C.uint64_t, C.UniffiRustFutureContinuationCallback, C.uint64_t)
type rustFutureCompleteFunc[T any] func(C.uint64_t, *C.RustCallStatus) T
type rustFutureFreeFunc func(C.uint64_t)

//export iroh_uniffiFutureContinuationCallback
func iroh_uniffiFutureContinuationCallback(data C.uint64_t, pollResult C.int8_t) {
	h := cgo.Handle(uintptr(data))
	waiter := h.Value().(chan int8)
	waiter <- int8(pollResult)
}

func uniffiRustCallAsync[E any, T any, F any](
	errConverter BufReader[E],
	completeFunc rustFutureCompleteFunc[F],
	liftFunc func(F) T,
	rustFuture C.uint64_t,
	pollFunc rustFuturePollFunc,
	freeFunc rustFutureFreeFunc,
) (T, E) {
	defer freeFunc(rustFuture)

	pollResult := int8(-1)
	waiter := make(chan int8, 1)

	chanHandle := cgo.NewHandle(waiter)
	defer chanHandle.Delete()

	for pollResult != uniffiRustFuturePollReady {
		pollFunc(
			rustFuture,
			(C.UniffiRustFutureContinuationCallback)(C.iroh_uniffiFutureContinuationCallback),
			C.uint64_t(chanHandle),
		)
		pollResult = <-waiter
	}

	var goValue T
	ffiValue, err := rustCallWithError(errConverter, func(status *C.RustCallStatus) F {
		return completeFunc(rustFuture, status)
	})
	if value := reflect.ValueOf(err); value.IsValid() && !value.IsZero() {
		return goValue, err
	}
	return liftFunc(ffiValue), err
}

//export iroh_uniffiFreeGorutine
func iroh_uniffiFreeGorutine(data C.uint64_t) {
	handle := cgo.Handle(uintptr(data))
	defer handle.Delete()

	guard := handle.Value().(chan struct{})
	guard <- struct{}{}
}

// Set the logging level.
func SetLogLevel(level LogLevel) {
	rustCall(func(_uniffiStatus *C.RustCallStatus) bool {
		C.uniffi_iroh_ffi_fn_func_set_log_level(FfiConverterLogLevelINSTANCE.Lower(level), _uniffiStatus)
		return false
	})
}

// The minimal preset (no external dependencies; good for tests / offline).
func PresetMinimal() Preset {
	return FfiConverterPresetINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_func_preset_minimal(_uniffiStatus)
	}))
}

// The n0 production preset (relays + discovery).
func PresetN0() Preset {
	return FfiConverterPresetINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_func_preset_n0(_uniffiStatus)
	}))
}

// The n0 preset with relays disabled.
func PresetN0DisableRelay() Preset {
	return FfiConverterPresetINSTANCE.Lift(rustCall(func(_uniffiStatus *C.RustCallStatus) C.uint64_t {
		return C.uniffi_iroh_ffi_fn_func_preset_n0_disable_relay(_uniffiStatus)
	}))
}
