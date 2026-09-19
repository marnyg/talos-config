package fakeip

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Zone is the presentation zone: <name>.mesh.internal. Inherited from
// v2 (spike eda): every existing certSAN carries it, and the plane has
// no opinion — v3 names are bare (cp1, hub, laptop). The zone exists
// only where a resolver needs an FQDN.
const Zone = "mesh.internal."

const (
	dnsTimeout = 3 * time.Second
	fakeTTL    = 60
)

// Directory answers "is this a name the plane knows right now" — the
// gate on minting a fake IP. On the desktop it is the agent's name map;
// on mobile (P0.2) it was "everything" because one peer existed.
type Directory interface {
	Known(name string) bool
}

// DirectoryFunc adapts a func to Directory.
type DirectoryFunc func(name string) bool

func (f DirectoryFunc) Known(name string) bool { return f(name) }

// Stats are the resolver's counters, for a status surface.
type Stats struct {
	Mesh     atomic.Int64 // in-zone, known: answered with a fake IP
	Forward  atomic.Int64 // sent upstream (out of zone, or in zone but unknown)
	Failed   atomic.Int64 // upstream gave nothing
	Refused  atomic.Int64 // in zone, unknown, no upstream: NXDOMAIN
	Malformd atomic.Int64
}

// Resolver is the split-DNS side of the fiction. It answers A queries
// for in-zone names the Directory knows with a stable fake IP, and
// forwards everything else — out-of-zone names, and in-zone names it
// does not know — to Upstreams. That second case is what makes the
// zone shareable during Phase 2/3: nebula's DNS keeps serving
// jackett.cp1.mesh.internal while cp1.mesh.internal has moved, so the
// name is the migration boundary, one consumer at a time.
//
// With no Upstreams, unknown in-zone names get NXDOMAIN and out-of-zone
// names are dropped (the client retries as with any lost datagram);
// that is the endgame after nebula is gone.
type Resolver struct {
	dir       Directory
	upstreams []netip.AddrPort
	control   func(network, address string, c syscall.RawConn) error
	Stats     Stats

	mu     sync.Mutex
	byName map[string]netip.Addr
	byIP   map[netip.Addr]string
	next   netip.Addr
}

// ResolverOptions configures NewResolver.
type ResolverOptions struct {
	Directory Directory
	// Upstreams is "ip[:port][,ip[:port]]": where non-mesh (and unknown
	// in-zone) queries go. Bare address → :53.
	Upstreams string
	// DialControl, if set, runs on every upstream socket before connect
	// (mobile: VpnService.protect so the forwarder cannot loop into the tun).
	DialControl func(network, address string, c syscall.RawConn) error
}

func NewResolver(o ResolverOptions) (*Resolver, error) {
	if o.Directory == nil {
		return nil, errors.New("fakeip: resolver needs a Directory")
	}
	r := &Resolver{
		dir: o.Directory, control: o.DialControl,
		byName: map[string]netip.Addr{}, byIP: map[netip.Addr]string{}, next: poolStart,
	}
	for _, s := range strings.Split(o.Upstreams, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ap, err := netip.ParseAddrPort(s)
		if err != nil { // bare address → :53
			addr, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return nil, fmt.Errorf("fakeip: upstream DNS %q: %w", s, err)
			}
			ap = netip.AddrPortFrom(addr, 53)
		}
		r.upstreams = append(r.upstreams, ap)
	}
	return r, nil
}

// Names is the current name → fake IP table, sorted.
func (r *Resolver) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.byName))
	for n, ip := range r.byName {
		out = append(out, n+" → "+ip.String())
	}
	sort.Strings(out)
	return out
}

// NameFor is the reverse map: which bare name a fake IP was minted for.
// This is how a TCP flow to a fake IP finds its member.
func (r *Resolver) NameFor(ip netip.Addr) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.byIP[ip]
	return n, ok
}

// InZone reports whether fqdn (with or without trailing dot) is under
// Zone, and returns the bare name if so.
func InZone(fqdn string) (bare string, ok bool) {
	n := strings.ToLower(fqdn)
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	if n == Zone || !strings.HasSuffix(n, "."+Zone) {
		return "", false
	}
	return strings.TrimSuffix(n, "."+Zone), true
}

// lookup mints or returns the fake IP for a bare name the Directory
// knows. The table only grows; a name's address never changes.
func (r *Resolver) lookup(bare string) (netip.Addr, bool) {
	if !r.dir.Known(bare) {
		return netip.Addr{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ip, ok := r.byName[bare]; ok {
		return ip, true
	}
	ip := r.next
	if !fakePrefix.Contains(ip) {
		return netip.Addr{}, false // pool exhausted: 131k names, not happening
	}
	r.next = ip.Next()
	r.byName[bare] = ip
	r.byIP[ip] = bare
	log.Printf("fakeip: dns %s → %s", bare, ip)
	return ip, true
}

// HandleUDP is the stack's UDP callback: one query in, one reply out
// (nil = drop).
func (r *Resolver) HandleUDP(src, dst netip.AddrPort, payload []byte) []byte {
	var p dnsmessage.Parser
	hdr, err := p.Start(payload)
	if err != nil {
		r.Stats.Malformd.Add(1)
		return nil
	}
	q, err := p.Question()
	if err != nil {
		r.Stats.Malformd.Add(1)
		return nil
	}
	if bare, ok := InZone(q.Name.String()); ok {
		switch {
		case q.Type == dnsmessage.TypeA:
			if ip, ok := r.lookup(bare); ok {
				r.Stats.Mesh.Add(1)
				return r.answer(hdr, q, ip, dnsmessage.RCodeSuccess)
			}
		case r.dir.Known(bare): // AAAA etc. for a known name: empty NOERROR, mint nothing
			r.Stats.Mesh.Add(1)
			return r.answer(hdr, q, netip.Addr{}, dnsmessage.RCodeSuccess)
		}
		if len(r.upstreams) == 0 {
			r.Stats.Refused.Add(1)
			return r.answer(hdr, q, netip.Addr{}, dnsmessage.RCodeNameError)
		}
	}
	r.Stats.Forward.Add(1)
	resp, err := r.exchange(payload)
	if err != nil {
		r.Stats.Failed.Add(1)
		return nil
	}
	return resp
}

// answer synthesizes the reply: A → the fake IP; AAAA / anything else →
// NOERROR with no answers (so clients fall through to A instead of
// waiting on a timeout — the same choice the v2 dnsshim makes). With
// rcode != success the answer section is empty.
func (r *Resolver) answer(hdr dnsmessage.Header, q dnsmessage.Question, ip netip.Addr, rcode dnsmessage.RCode) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: hdr.ID, Response: true, Authoritative: true, RecursionDesired: hdr.RecursionDesired, RecursionAvailable: true,
		RCode: rcode,
	})
	b.EnableCompression()
	_ = b.StartQuestions()
	_ = b.Question(q)
	_ = b.StartAnswers()
	if rcode == dnsmessage.RCodeSuccess && q.Type == dnsmessage.TypeA && ip.IsValid() {
		_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: fakeTTL},
			dnsmessage.AResource{A: ip.As4()})
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// exchange forwards a query to the upstreams, first answer wins.
func (r *Resolver) exchange(query []byte) ([]byte, error) {
	for _, up := range r.upstreams {
		dialer := net.Dialer{Timeout: dnsTimeout, Control: r.control}
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
