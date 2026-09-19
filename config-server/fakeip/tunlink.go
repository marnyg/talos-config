package fakeip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// TunLink is a gvisor link endpoint over a wireguard-go tun.Device —
// the portable alternative to gvisor's fdbased (linux-only). Two
// goroutines pump packets: device reads are injected inbound, the
// stack's outbound queue is written to the device. The device's own
// framing (the 4-byte AF header on darwin utun) is its business:
// wireguard-go strips and adds it as long as we read and write with
// an offset, which is what tunOffset is for.
type TunLink struct {
	*channel.Endpoint
	dev    tun.Device
	cancel context.CancelFunc
	wg     sync.WaitGroup
	err    error
	once   sync.Once
	done   chan struct{}
}

// tunOffset is the headroom before each packet in the buffers handed to
// the device. wireguard-go's darwin tun needs ≥ 4 (the AF prefix), its
// linux tun ≥ the virtio header; 16 covers both and is what wireguard
// itself uses.
const tunOffset = 16

// maxPacket is the largest IP packet a tun can deliver, whatever its MTU
// says (linux with offloads coalesces past it).
const maxPacket = 65535

// NewTunLink starts pumping dev. queueLen bounds the outbound queue
// between the stack and the writer goroutine.
func NewTunLink(dev tun.Device, queueLen int) (*TunLink, error) {
	mtu, err := dev.MTU()
	if err != nil {
		return nil, fmt.Errorf("tun mtu: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &TunLink{
		Endpoint: channel.New(queueLen, uint32(mtu), ""),
		dev:      dev,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	l.wg.Add(2)
	go l.readLoop()
	go l.writeLoop(ctx)
	return l, nil
}

// Done is closed when either pump has stopped (the device went away, or
// Close was called). Err says why.
func (l *TunLink) Done() <-chan struct{} { return l.done }

// Err is the first pump error, or nil after a clean Close.
func (l *TunLink) Err() error { return l.err }

// Close stops both pumps, closes the device, then the endpoint.
func (l *TunLink) Close() {
	l.stop(nil)
	l.wg.Wait()
	l.Endpoint.Close()
}

func (l *TunLink) stop(err error) {
	l.once.Do(func() {
		l.err = err
		l.cancel()
		_ = l.dev.Close() // unblocks the reader
		close(l.done)
	})
}

// readLoop: device → stack. Each packet gets its own slice because the
// PacketBuffer takes ownership of its payload.
func (l *TunLink) readLoop() {
	defer l.wg.Done()
	batch := l.dev.BatchSize()
	bufs := make([][]byte, batch)
	for i := range bufs {
		bufs[i] = make([]byte, tunOffset+maxPacket)
	}
	sizes := make([]int, batch)
	for {
		n, err := l.dev.Read(bufs, sizes, tunOffset)
		for i := 0; i < n; i++ {
			pktb := bufs[i][tunOffset : tunOffset+sizes[i]]
			if len(pktb) < header.IPv4MinimumSize || header.IPVersion(pktb) != header.IPv4Version {
				continue // the stack is v4-only; nothing else is routed here
			}
			pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
				Payload: buffer.MakeWithData(append([]byte(nil), pktb...)),
			})
			l.InjectInbound(ipv4.ProtocolNumber, pkt)
			pkt.DecRef()
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, tun.ErrTooManySegments) {
				continue // wireguard-go: transient on linux offload paths
			}
			l.stop(fmt.Errorf("tun read: %w", err))
			return
		}
	}
}

// writeLoop: stack → device.
func (l *TunLink) writeLoop(ctx context.Context) {
	defer l.wg.Done()
	buf := make([]byte, tunOffset+maxPacket)
	for {
		pkt := l.ReadContext(ctx)
		if pkt == nil {
			return // ctx cancelled
		}
		n := tunOffset
		for _, s := range pkt.AsSlices() {
			n += copy(buf[n:], s)
		}
		pkt.DecRef()
		if _, err := l.dev.Write([][]byte{buf[:n]}, tunOffset); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("fakeip: tun write: %v", err)
		}
	}
}
