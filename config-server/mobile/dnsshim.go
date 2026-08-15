package mobile

// In-tunnel split DNS (task 3b1734db): Android routes *all* device DNS
// to a VPN-provided resolver, and the hub's resolver answers only the
// mesh zone — so pointing the tun at the hub would break every
// non-mesh lookup whenever the hub is sealed (every fly deploy). The
// fix lives entirely on-device: the VpnService advertises a magic
// resolver IP inside the mesh route (network base + 53, e.g.
// 10.42.0.53), and this Device wrapper intercepts UDP/53 packets to it
// before they enter nebula:
//
//   - queries for the mesh zone are rewritten to the hub's resolver
//     and travel through the tunnel like any other mesh packet (the
//     reply's source is rewritten back on the way in);
//   - everything else is forwarded over a *protected* underlay socket
//     (VpnService.protect via the SocketProtector callback, so the
//     forward doesn't loop back into the VPN) and the answer is
//     crafted into a raw IPv4/UDP packet written straight to the tun.
//
// The hub is unchanged and the design degrades safely: a sealed hub
// only costs mesh names, never the device's general DNS.
//
// TCP to the magic resolver is answered with an immediate RST (the
// Tailscale quad100 pattern, which gets this free from netstack):
// Android probes the VPN resolver for DNS-over-TLS on tcp/853, and a
// silently dropped SYN can stall resolver validation for the whole
// session — a fast RST makes it fall back to plain UDP at once. The
// same applies to DNS-over-TCP fallback: it fails fast (mesh answers
// are small enough that truncation should never trigger it).
//
// v1 limits: IPv4 only (the mesh is IPv4), UDP-only resolution (TCP
// gets RST, not service), zone hardcoded.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/slackhq/nebula/overlay"
)

// SocketProtector is implemented in Kotlin with VpnService.protect:
// it marks a socket fd as bypassing the VPN so underlay DNS forwards
// escape the tunnel instead of looping back into it.
type SocketProtector interface {
	Protect(fd int32) bool
}

const (
	// meshDNSZone is the zone the hub's resolver serves (trailing dot,
	// matching normalized qnames). Hardcoded for v1; surfaced to
	// Kotlin via ConfigInfo so there is one source of truth.
	meshDNSZone = "mesh.internal."

	// dnsMagicOffset: the magic resolver is mesh network base + 53.
	// Not a real host — the hub derives addresses from approved names
	// and starts at .1, so a collision would need ~13k devices.
	dnsMagicOffset = 53

	dnsPort    = 53
	dnsTimeout = 5 * time.Second

	protoTCP = 6
	protoUDP = 17
)

// dnsMagicIP derives the magic resolver address from the mesh network:
// network base + dnsMagicOffset (10.42.0.0/16 → 10.42.0.53). Both the
// shim and ConfigInfo (Kotlin's addDnsServer) use this, so they cannot
// disagree.
func dnsMagicIP(network netip.Prefix) (netip.Addr, error) {
	if !network.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("mesh network %s is not IPv4", network)
	}
	base := network.Masked().Addr().As4()
	v := binary.BigEndian.Uint32(base[:]) + dnsMagicOffset
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], v)
	return netip.AddrFrom4(out), nil
}

// dnsDevice wraps the fd-backed tun device nebula runs on. Read/Write
// are the packet paths; everything else passes through.
type dnsDevice struct {
	overlay.Device
	magicIP   netip.Addr
	hubIP     netip.Addr
	zone      string // trailing dot, e.g. "mesh.internal."
	protector SocketProtector

	mu        sync.Mutex // guards upstreams: roaming swaps them mid-flight
	upstreams []netip.AddrPort
}

// setUpstreams replaces the underlay resolvers. Called (via
// Tunnel.SetUpstreams) from Kotlin's ConnectivityManager callback when
// the underlying network changes — the resolvers captured at
// establish() go stale the moment the device roams wifi↔cellular.
func (d *dnsDevice) setUpstreams(ups []netip.AddrPort) {
	if len(ups) == 0 {
		return
	}
	d.mu.Lock()
	d.upstreams = ups
	d.mu.Unlock()
}

func (d *dnsDevice) getUpstreams() []netip.AddrPort {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.upstreams
}

func newDNSDevice(inner overlay.Device, hubIP netip.Addr, upstreams []netip.AddrPort, protector SocketProtector) (*dnsDevice, error) {
	var network netip.Prefix
	for _, p := range inner.Networks() {
		if p.Addr().Is4() {
			network = p
			break
		}
	}
	if !network.IsValid() {
		return nil, errors.New("tun device has no IPv4 network")
	}
	magic, err := dnsMagicIP(network)
	if err != nil {
		return nil, err
	}
	if len(upstreams) == 0 {
		return nil, errors.New("no upstream DNS resolvers")
	}
	return &dnsDevice{
		Device:    inner,
		magicIP:   magic,
		hubIP:     hubIP,
		zone:      meshDNSZone,
		upstreams: upstreams,
		protector: protector,
	}, nil
}

// Read is the OS→nebula path. Queries to the magic resolver are peeled
// off: mesh-zone names are rewritten toward the hub (and returned to
// nebula to travel the tunnel); everything else forwards on the
// underlay and the loop continues with the next packet — nebula never
// sees it.
func (d *dnsDevice) Read(b []byte) (int, error) {
	for {
		n, err := d.Device.Read(b)
		if err != nil {
			return n, err
		}
		pkt := b[:n]
		ip, ok := parseIPv4(pkt)
		if !ok {
			return n, nil
		}
		// TCP to the magic resolver: nothing listens there, ever —
		// reset it so DoT probes and TCP-fallback fail fast instead of
		// dangling SYNs into nebula (see the package comment).
		if ip.proto == protoTCP && ip.dst == d.magicIP {
			if rst := buildTCPRst(pkt, ip); rst != nil {
				d.Device.Write(rst) //nolint:errcheck // best-effort, like any RST
			}
			continue // consumed either way; nebula never sees it
		}
		m, ok := parseIPv4UDP(pkt)
		if !ok || m.dst != d.magicIP || m.dport != dnsPort {
			return n, nil
		}
		name, ok := dnsQName(m.payload)
		if ok && (name == d.zone || strings.HasSuffix(name, "."+d.zone)) {
			rewriteIPv4UDPAddr(pkt, m.ihl, 16, d.hubIP) // dst addr at offset 16
			return n, nil
		}
		// Underlay path: copy what outlives this Read's buffer.
		query := make([]byte, len(m.payload))
		copy(query, m.payload)
		go d.forwardUnderlay(m.src, m.sport, query)
	}
}

// Write is the nebula→OS path. The hub's DNS answers come back
// addressed from hubIP:53 (we rewrote the query); restore the magic
// source so the client's socket recognizes the reply.
func (d *dnsDevice) Write(b []byte) (int, error) {
	if m, ok := parseIPv4UDP(b); ok && m.src == d.hubIP && m.sport == dnsPort {
		rewriteIPv4UDPAddr(b, m.ihl, 12, d.magicIP) // src addr at offset 12
	}
	return d.Device.Write(b)
}

// SupportsMultiqueue is forced off: multiqueue readers would bypass
// this wrapper's Read. The app runs one routine anyway.
func (d *dnsDevice) SupportsMultiqueue() bool { return false }

// forwardUnderlay exchanges one query with the upstream resolvers over
// a protected socket and injects the answer into the tun as a packet
// from the magic resolver. Failures are silent drops — the client
// retries, exactly as with a lost UDP datagram.
func (d *dnsDevice) forwardUnderlay(clientIP netip.Addr, clientPort uint16, query []byte) {
	resp, err := d.exchange(query)
	if err != nil {
		return
	}
	pkt := buildIPv4UDP(d.magicIP, dnsPort, clientIP, clientPort, resp)
	d.Device.Write(pkt) //nolint:errcheck // drop-on-failure, like any UDP
}

func (d *dnsDevice) exchange(query []byte) ([]byte, error) {
	for _, up := range d.getUpstreams() {
		dialer := net.Dialer{Timeout: dnsTimeout, Control: d.protectControl}
		conn, err := dialer.Dial("udp", up.String())
		if err != nil {
			continue
		}
		conn.SetDeadline(time.Now().Add(dnsTimeout)) //nolint:errcheck
		if _, err := conn.Write(query); err != nil {
			conn.Close()
			continue
		}
		buf := make([]byte, 1500)
		n, err := conn.Read(buf)
		conn.Close()
		if err != nil {
			continue
		}
		return buf[:n], nil
	}
	return nil, errors.New("no upstream answered")
}

// protectControl runs at socket creation, before connect — the window
// VpnService.protect needs.
func (d *dnsDevice) protectControl(network, address string, c syscall.RawConn) error {
	if d.protector == nil {
		return nil
	}
	var perr error
	if err := c.Control(func(fd uintptr) {
		if !d.protector.Protect(int32(fd)) {
			perr = errors.New("VpnService.protect refused the socket")
		}
	}); err != nil {
		return err
	}
	return perr
}

// --- packet plumbing (pure functions, unit-tested off-device) ---

type ipv4Info struct {
	ihl, total int
	proto      byte
	src, dst   netip.Addr
}

// parseIPv4 returns the network layer of an unfragmented IPv4 packet,
// or ok=false for anything else (v6, fragments, runts).
func parseIPv4(pkt []byte) (ipv4Info, bool) {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return ipv4Info{}, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl {
		return ipv4Info{}, false
	}
	if binary.BigEndian.Uint16(pkt[6:8])&0x3fff != 0 { // MF or offset: fragment
		return ipv4Info{}, false
	}
	total := int(binary.BigEndian.Uint16(pkt[2:4]))
	if total < ihl || total > len(pkt) {
		return ipv4Info{}, false
	}
	return ipv4Info{
		ihl:   ihl,
		total: total,
		proto: pkt[9],
		src:   netip.AddrFrom4([4]byte(pkt[12:16])),
		dst:   netip.AddrFrom4([4]byte(pkt[16:20])),
	}, true
}

type udpMeta struct {
	ihl          int
	src, dst     netip.Addr
	sport, dport uint16
	payload      []byte
}

// parseIPv4UDP returns the addressing of an unfragmented IPv4/UDP
// packet, or ok=false for anything else.
func parseIPv4UDP(pkt []byte) (udpMeta, bool) {
	ip, ok := parseIPv4(pkt)
	if !ok || ip.proto != protoUDP || ip.total < ip.ihl+8 {
		return udpMeta{}, false
	}
	ihl := ip.ihl
	ulen := int(binary.BigEndian.Uint16(pkt[ihl+4 : ihl+6]))
	if ulen < 8 || ihl+ulen > ip.total {
		return udpMeta{}, false
	}
	return udpMeta{
		ihl:     ihl,
		src:     ip.src,
		dst:     ip.dst,
		sport:   binary.BigEndian.Uint16(pkt[ihl : ihl+2]),
		dport:   binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4]),
		payload: pkt[ihl+8 : ihl+ulen],
	}, true
}

// buildTCPRst answers a TCP segment aimed at the magic resolver with
// the RFC 9293 reset for a closed port: RST/ACK acking SEG.SEQ+SEG.LEN
// when the segment had no ACK (a SYN), plain RST at SEQ=SEG.ACK when
// it did. Returns nil for segments that must not be reset (a RST
// itself) or that don't parse.
func buildTCPRst(pkt []byte, ip ipv4Info) []byte {
	if ip.total < ip.ihl+20 {
		return nil
	}
	tcp := pkt[ip.ihl:ip.total]
	dataOff := int(tcp[12]>>4) * 4
	if dataOff < 20 || dataOff > len(tcp) {
		return nil
	}
	flags := tcp[13]
	if flags&0x04 != 0 { // never reset a reset
		return nil
	}
	segLen := uint32(len(tcp) - dataOff)
	if flags&0x02 != 0 { // SYN counts
		segLen++
	}
	if flags&0x01 != 0 { // FIN counts
		segLen++
	}

	out := make([]byte, 40)
	out[0] = 0x45
	binary.BigEndian.PutUint16(out[2:4], 40)
	out[8] = 64
	out[9] = protoTCP
	s4, d4 := ip.dst.As4(), ip.src.As4() // reply: swap
	copy(out[12:16], s4[:])
	copy(out[16:20], d4[:])
	binary.BigEndian.PutUint16(out[10:12], ipChecksum(out[:20]))

	t := out[20:]
	copy(t[0:2], tcp[2:4]) // our src port = their dst port
	copy(t[2:4], tcp[0:2])
	t[12] = 0x50         // data offset 20
	if flags&0x10 != 0 { // segment had ACK
		copy(t[4:8], tcp[8:12]) // SEQ = SEG.ACK
		t[13] = 0x04            // RST
	} else {
		binary.BigEndian.PutUint32(t[8:12], binary.BigEndian.Uint32(tcp[4:8])+segLen)
		t[13] = 0x14 // RST|ACK
	}
	binary.BigEndian.PutUint16(t[16:18], transportChecksum(out, 20, protoTCP))
	return out
}

// dnsQName extracts the first question name from a DNS message,
// normalized to lowercase with a trailing dot ("jellyfin.mesh.internal.").
func dnsQName(msg []byte) (string, bool) {
	if len(msg) < 12 || binary.BigEndian.Uint16(msg[4:6]) == 0 {
		return "", false
	}
	var sb strings.Builder
	i := 12
	for {
		if i >= len(msg) {
			return "", false
		}
		l := int(msg[i])
		if l == 0 {
			break
		}
		if l >= 0xc0 { // compression pointer: invalid in a question name
			return "", false
		}
		i++
		if i+l > len(msg) {
			return "", false
		}
		for _, c := range msg[i : i+l] {
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			sb.WriteByte(c)
		}
		sb.WriteByte('.')
		i += l
	}
	return sb.String(), true
}

// rewriteIPv4UDPAddr replaces the IPv4 address at pkt[off:off+4]
// (12=src, 16=dst) and recomputes both checksums.
func rewriteIPv4UDPAddr(pkt []byte, ihl, off int, addr netip.Addr) {
	a := addr.As4()
	copy(pkt[off:off+4], a[:])
	pkt[10], pkt[11] = 0, 0
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:ihl]))
	pkt[ihl+6], pkt[ihl+7] = 0, 0
	binary.BigEndian.PutUint16(pkt[ihl+6:ihl+8], transportChecksum(pkt, ihl, protoUDP))
}

// buildIPv4UDP crafts a complete IPv4/UDP packet (no options, DF
// clear, TTL 64) carrying payload.
func buildIPv4UDP(src netip.Addr, sport uint16, dst netip.Addr, dport uint16, payload []byte) []byte {
	total := 20 + 8 + len(payload)
	pkt := make([]byte, total)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	pkt[8] = 64
	pkt[9] = 17
	s4, d4 := src.As4(), dst.As4()
	copy(pkt[12:16], s4[:])
	copy(pkt[16:20], d4[:])
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:20]))
	binary.BigEndian.PutUint16(pkt[20:22], sport)
	binary.BigEndian.PutUint16(pkt[22:24], dport)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(8+len(payload)))
	copy(pkt[28:], payload)
	binary.BigEndian.PutUint16(pkt[26:28], transportChecksum(pkt, 20, protoUDP))
	return pkt
}

// ipChecksum computes the IPv4 header checksum; the checksum field
// must be zeroed by the caller.
func ipChecksum(hdr []byte) uint16 {
	return ^foldSum(onesSum(hdr, 0))
}

// transportChecksum computes the TCP/UDP checksum over pseudo-header +
// segment; the checksum field must be zeroed by the caller. For UDP it
// never returns 0 (RFC 768: 0 means "no checksum").
func transportChecksum(pkt []byte, ihl int, proto byte) uint16 {
	seg := pkt[ihl:]
	var pseudo [12]byte
	copy(pseudo[0:8], pkt[12:20])
	pseudo[9] = proto
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(seg)))
	ck := ^foldSum(onesSum(seg, onesSum(pseudo[:], 0)))
	if proto == protoUDP && ck == 0 {
		ck = 0xffff
	}
	return ck
}

func onesSum(b []byte, sum uint32) uint32 {
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	return sum
}

func foldSum(sum uint32) uint16 {
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return uint16(sum)
}
