package fakeip

import (
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func query(t *testing.T, name string, typ dnsmessage.Type) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	_ = b.StartQuestions()
	if err := b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET}); err != nil {
		t.Fatal(err)
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func parse(t *testing.T, resp []byte) (dnsmessage.Header, []netip.Addr) {
	t.Helper()
	var p dnsmessage.Parser
	hdr, err := p.Start(resp)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SkipAllQuestions(); err != nil {
		t.Fatal(err)
	}
	var ips []netip.Addr
	for {
		rr, err := p.AnswerHeader()
		if err == dnsmessage.ErrSectionDone {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if rr.Type == dnsmessage.TypeA {
			a, err := p.AResource()
			if err != nil {
				t.Fatal(err)
			}
			ips = append(ips, netip.AddrFrom4(a.A))
		} else {
			_ = p.SkipAnswer()
		}
	}
	return hdr, ips
}

var src, dst = netip.MustParseAddrPort("198.18.0.1:5555"), netip.MustParseAddrPort(ResolverIP + ":53")

func TestInZone(t *testing.T) {
	for in, want := range map[string]string{
		"cp1.mesh.internal.":         "cp1",
		"CP1.Mesh.Internal":          "cp1",
		"jackett.cp1.mesh.internal.": "jackett.cp1",
		"mesh.internal.":             "",
		"cp1.mesh.internal.evil.":    "",
		"example.com.":               "",
	} {
		got, ok := InZone(in)
		if ok != (want != "") || got != want {
			t.Errorf("InZone(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}

// Known in-zone names get a stable fake IP in the pool and a reverse
// entry; the same name always gets the same address.
func TestKnownNameGetsStableFakeIP(t *testing.T) {
	r, err := NewResolver(ResolverOptions{Directory: DirectoryFunc(func(n string) bool { return n == "cp1" })})
	if err != nil {
		t.Fatal(err)
	}
	_, ips1 := parse(t, r.HandleUDP(src, dst, query(t, "cp1.mesh.internal.", dnsmessage.TypeA)))
	_, ips2 := parse(t, r.HandleUDP(src, dst, query(t, "CP1.mesh.internal.", dnsmessage.TypeA)))
	if len(ips1) != 1 || len(ips2) != 1 || ips1[0] != ips2[0] {
		t.Fatalf("want one stable A, got %v then %v", ips1, ips2)
	}
	if !fakePrefix.Contains(ips1[0]) || ips1[0] != poolStart {
		t.Errorf("first fake IP = %v, want %v", ips1[0], poolStart)
	}
	if n, ok := r.NameFor(ips1[0]); !ok || n != "cp1" {
		t.Errorf("NameFor(%v) = %q,%v", ips1[0], n, ok)
	}
	if r.Stats.Mesh.Load() != 2 {
		t.Errorf("Mesh = %d, want 2", r.Stats.Mesh.Load())
	}
}

// AAAA for a known name: NOERROR, no answers — the client falls through
// to A instead of waiting on a timeout.
func TestAAAAIsEmptyNoError(t *testing.T) {
	r, _ := NewResolver(ResolverOptions{Directory: DirectoryFunc(func(string) bool { return true })})
	hdr, ips := parse(t, r.HandleUDP(src, dst, query(t, "cp1.mesh.internal.", dnsmessage.TypeAAAA)))
	if hdr.RCode != dnsmessage.RCodeSuccess || len(ips) != 0 {
		t.Errorf("rcode %v ips %v", hdr.RCode, ips)
	}
	if _, ok := r.NameFor(poolStart); ok {
		t.Error("AAAA must not mint a fake IP")
	}
}

// The gate: an in-zone name the plane does not know is not minted. With
// no upstream it is NXDOMAIN (the endgame); with one it is forwarded
// (Phase 2/3 coexistence — nebula's DNS still owns that name).
func TestUnknownInZoneNameIsGated(t *testing.T) {
	none := DirectoryFunc(func(string) bool { return false })
	r, _ := NewResolver(ResolverOptions{Directory: none})
	hdr, ips := parse(t, r.HandleUDP(src, dst, query(t, "jackett.cp1.mesh.internal.", dnsmessage.TypeA)))
	if hdr.RCode != dnsmessage.RCodeNameError || len(ips) != 0 {
		t.Errorf("no upstream: rcode %v ips %v, want NXDOMAIN", hdr.RCode, ips)
	}
	if len(r.Names()) != 0 {
		t.Errorf("minted for an unknown name: %v", r.Names())
	}

	// An upstream that never answers (unroutable, short timeout is the
	// resolver's) — we only assert the query was forwarded, not answered.
	r, _ = NewResolver(ResolverOptions{Directory: none, Upstreams: "127.0.0.1:1"})
	if resp := r.HandleUDP(src, dst, query(t, "jackett.cp1.mesh.internal.", dnsmessage.TypeA)); resp != nil {
		t.Errorf("dead upstream answered: %x", resp)
	}
	if r.Stats.Forward.Load() != 1 || r.Stats.Failed.Load() != 1 || r.Stats.Refused.Load() != 0 {
		t.Errorf("stats fwd=%d failed=%d refused=%d", r.Stats.Forward.Load(), r.Stats.Failed.Load(), r.Stats.Refused.Load())
	}
}

func TestUpstreamParsing(t *testing.T) {
	r, err := NewResolver(ResolverOptions{Directory: DirectoryFunc(func(string) bool { return true }), Upstreams: "10.42.0.1, 10.42.0.2:5353"})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.AddrPort{netip.MustParseAddrPort("10.42.0.1:53"), netip.MustParseAddrPort("10.42.0.2:5353")}
	if len(r.upstreams) != 2 || r.upstreams[0] != want[0] || r.upstreams[1] != want[1] {
		t.Errorf("upstreams = %v, want %v", r.upstreams, want)
	}
	if _, err := NewResolver(ResolverOptions{Directory: r.dir, Upstreams: "nope"}); err == nil {
		t.Error("bad upstream accepted")
	}
	if _, err := NewResolver(ResolverOptions{}); err == nil {
		t.Error("nil Directory accepted")
	}
}
