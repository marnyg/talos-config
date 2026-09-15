package p0mobile

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Fake-IP plan (RFC 2544 benchmark range, the plan's 198.18.0.0/15):
//
//	198.18.0.1   the tun's own address (Kotlin: Builder.addAddress)
//	198.18.0.2   the resolver the tun advertises (Builder.addDnsServer)
//	198.18.1.0+  one address per *.mesh.internal name, first-seen order
//
// Only this /15 is routed into the tun. A name's fake IP is stable for
// the life of the tunnel (Jellyfin caches the server address).
const (
	FakeRange  = "198.18.0.0/15"
	TunIP      = "198.18.0.1"
	ResolverIP = "198.18.0.2"
	MeshZone   = "mesh.internal."
	dnsTimeout = 3 * time.Second
	fakeTTL    = 60
)

var poolStart = netip.MustParseAddr("198.18.1.0")

type fakeDNS struct {
	upstreams []netip.AddrPort
	protector SocketProtector
	st        *stats

	mu     sync.Mutex
	byName map[string]netip.Addr
	next   netip.Addr
}

func newFakeDNS(upstreamDNS string, protector SocketProtector, st *stats) (*fakeDNS, error) {
	d := &fakeDNS{protector: protector, st: st, byName: map[string]netip.Addr{}, next: poolStart}
	for _, s := range strings.Split(upstreamDNS, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ap, err := netip.ParseAddrPort(s)
		if err != nil { // bare address → :53
			addr, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return nil, fmt.Errorf("upstream DNS %q: %w", s, err)
			}
			ap = netip.AddrPortFrom(addr, 53)
		}
		d.upstreams = append(d.upstreams, ap)
	}
	if len(d.upstreams) == 0 {
		log.Printf("dns: no upstreams — non-mesh names will fail while the tunnel is up")
	}
	return d, nil
}

func (d *fakeDNS) close() {}

// names is the current name → fake IP table, for the activity.
func (d *fakeDNS) names() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.byName))
	for n, ip := range d.byName {
		out = append(out, n+" → "+ip.String())
	}
	sort.Strings(out)
	return out
}

func (d *fakeDNS) lookup(name string) netip.Addr {
	name = strings.ToLower(name)
	d.mu.Lock()
	defer d.mu.Unlock()
	if ip, ok := d.byName[name]; ok {
		return ip
	}
	ip := d.next
	d.next = d.next.Next()
	d.byName[name] = ip
	log.Printf("dns: %s → %s", name, ip)
	return ip
}

// handleUDP is the netstack's UDP callback: one query in, one reply out
// (nil = drop; the client retries as with any lost datagram).
func (d *fakeDNS) handleUDP(src, dst netip.AddrPort, payload []byte) []byte {
	var p dnsmessage.Parser
	hdr, err := p.Start(payload)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	name := q.Name.String()
	if strings.HasSuffix(strings.ToLower(name), MeshZone) {
		d.st.DNSMesh.Add(1)
		return d.answerMesh(hdr, q, name)
	}
	d.st.DNSUnderlay.Add(1)
	resp, err := d.exchange(payload)
	if err != nil {
		d.st.DNSFail.Add(1)
		return nil
	}
	return resp
}

// answerMesh synthesizes the reply: A → fake IP; AAAA / anything else →
// NOERROR with no answers (so clients fall through to A instead of
// waiting on a timeout — the same choice the shipped dnsshim makes).
func (d *fakeDNS) answerMesh(hdr dnsmessage.Header, q dnsmessage.Question, name string) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: hdr.ID, Response: true, Authoritative: true, RecursionDesired: hdr.RecursionDesired, RecursionAvailable: true,
	})
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	if q.Type == dnsmessage.TypeA {
		ip := d.lookup(strings.TrimSuffix(name, "."))
		_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: fakeTTL},
			dnsmessage.AResource{A: ip.As4()})
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// exchange forwards a non-mesh query to the underlay resolvers over a
// protected socket, first answer wins (dnsshim's exchange).
func (d *fakeDNS) exchange(query []byte) ([]byte, error) {
	for _, up := range d.upstreams {
		dialer := net.Dialer{Timeout: dnsTimeout, Control: d.protectControl}
		conn, err := dialer.Dial("udp", up.String())
		if err != nil {
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(dnsTimeout))
		if _, err := conn.Write(query); err != nil {
			conn.Close()
			continue
		}
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		conn.Close()
		if err != nil {
			continue
		}
		return buf[:n], nil
	}
	return nil, errors.New("no upstream answered")
}

func (d *fakeDNS) protectControl(network, address string, c syscall.RawConn) error {
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
