package issuer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/marnyg/talos-config/protocol/actor"
	"github.com/marnyg/talos-config/protocol/cert"
)

// bundleAt runs the #bundle handler for member m (from its own key) and
// decodes the reply.
func bundleAt(t *testing.T, iss *Issuer, m cert.Cert, proof ...cert.Cert) Bundle {
	t.Helper()
	body, err := iss.bundleHandler(context.Background(), invocation(t, cert.ActorID(m.Aud), bundleReq(t, m), proof...))
	if err != nil {
		t.Fatalf("bundle for %s: %v", m.Cav.Name, err)
	}
	b, err := DecodeBundle(body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func names(entries []NameEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Member.Cav.Name)
	}
	return out
}

// ---- 1. the map is witnessed on the beat -----------------------------------

// TestNameMapWitnessesMembers: nothing is in the map until a member
// beats; each #bundle adds its caller; the map is sorted by name; a
// blocklisted member drops out of the map on the same beat it stops
// being served; an expired cert is evicted.
func TestNameMapWitnessesMembers(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	iss.Policy = filePolicy(t, testRecipe)
	w.unseal(t, iss)

	laptop, err := iss.Mint(newNode(t).ActorID(), "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	tv, err := iss.Mint(newNode(t).ActorID(), "tv", []string{"media"})
	if err != nil {
		t.Fatal(err)
	}
	cp1, err := iss.Mint(newNode(t).ActorID(), "cp1", []string{"machines"})
	if err != nil {
		t.Fatal(err)
	}

	// Minting is not witnessing: a kit handed out but never used is not
	// in the map. The first beat's map holds exactly its caller.
	b := bundleAt(t, iss, tv.Member)
	if got := names(b.NameMap); !slices.Equal(got, []string{"tv"}) {
		t.Fatalf("first beat map = %v, want [tv]", got)
	}
	if b.NameMap[0].Location != nil {
		t.Fatalf("no envelope carried a location, yet map has one: %+v", b.NameMap[0].Location)
	}
	bundleAt(t, iss, cp1.Member)
	b = bundleAt(t, iss, laptop.Member)
	if got := names(b.NameMap); !slices.Equal(got, []string{"cp1", "laptop", "tv"}) {
		t.Fatalf("map = %v, want sorted [cp1 laptop tv]", got)
	}
	// Each entry is the very cert the member presented — hubkey-signed,
	// so a receiver resolves it through the same speak-as.
	for _, e := range b.NameMap {
		if e.Member.Iss != iss.ID() || cert.Verify(e.Member) != nil {
			t.Fatalf("entry is not this hub's member cert: %+v", e.Member)
		}
	}
	if got := Lookup(b.NameMap, "cp1"); len(got) != 1 || got[0].Member.Aud != cp1.Member.Aud {
		t.Fatalf("Lookup cp1 = %+v", got)
	}
	if got := Lookup(b.NameMap, "nas"); got != nil {
		t.Fatalf("Lookup of an unknown name = %+v", got)
	}

	// Blocklist tv: laptop's next map omits it, and the blocklist rides.
	iss.Policy = filePolicy(t, testRecipe, cert.ActorID(tv.Member.Aud))
	b = bundleAt(t, iss, laptop.Member)
	if got := names(b.NameMap); !slices.Equal(got, []string{"cp1", "laptop"}) {
		t.Fatalf("map with tv blocked = %v", got)
	}
	// Unblock: the witness was kept, the name resolves again without tv
	// having to beat (the filter is a projection, not a deletion).
	iss.Policy = filePolicy(t, testRecipe)
	if got := names(bundleAt(t, iss, laptop.Member).NameMap); !slices.Equal(got, []string{"cp1", "laptop", "tv"}) {
		t.Fatalf("map after unblock = %v", got)
	}

	// cp1 renews at day 60 (a fresh cert, later iat); laptop and tv let
	// theirs lapse. Past day 90 only cp1's renewed cert is left.
	clk.Advance(60 * Day)
	cp1r, err := iss.Mint(cert.ActorID(cp1.Member.Aud), "cp1", []string{"machines"})
	if err != nil {
		t.Fatal(err)
	}
	bundleAt(t, iss, cp1r.Member)
	clk.Advance(31 * Day)
	// The Issuer's speak-as is 120 d; at day 91 it is inside the nag
	// window, so re-unseal to keep serving.
	w.unseal(t, iss)
	b = bundleAt(t, iss, cp1r.Member)
	if got := names(b.NameMap); !slices.Equal(got, []string{"cp1"}) {
		t.Fatalf("map after expiry = %v, want [cp1]", got)
	}
	if b.NameMap[0].Member.Iat != cp1r.Member.Iat {
		t.Fatal("map kept the old cp1 cert over the renewed one")
	}
}

// TestNameMapNewestCertWins: a member beating with a stale kit at a hub
// that already witnessed its renewal does not roll the binding back;
// a re-key (new NodeId, same name) shows both until the old lapses.
func TestNameMapNewestCertWins(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	iss.Policy = filePolicy(t, testRecipe)
	w.unseal(t, iss)
	N := newNode(t).ActorID()
	old, err := iss.Mint(N, "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(Day)
	renewed, err := iss.Mint(N, "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	bundleAt(t, iss, renewed.Member)
	b := bundleAt(t, iss, old.Member) // stale kit, still valid
	if len(b.NameMap) != 1 || b.NameMap[0].Member.Iat != renewed.Member.Iat {
		t.Fatalf("stale cert rolled the map back: %+v", b.NameMap)
	}

	// Re-key: a second NodeId enrolled under the same name.
	M := newNode(t).ActorID()
	rekeyed, err := iss.Mint(M, "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	b = bundleAt(t, iss, rekeyed.Member)
	if got := Lookup(b.NameMap, "laptop"); len(got) != 2 {
		t.Fatalf("re-key: want both keys under laptop until the old lapses, got %+v", got)
	}
}

// ---- 2. wire ------------------------------------------------------------------

func TestNameMapWire(t *testing.T) {
	clk := newClock()
	w := newWallet(t)
	iss := newIssuer(t, clk, nil)
	w.unseal(t, iss)
	node := newNode(t)
	kit, err := iss.Mint(node.ActorID(), "laptop", []string{"admins"})
	if err != nil {
		t.Fatal(err)
	}
	now := clk.Now()
	loc, err := cert.Sign(cert.Cert{
		Aud: cert.AudAny, Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Endpoints: []string{"iroh:relay=https://hub.example"}},
		Iat: now, Exp: now + 3600,
	}, node)
	if err != nil {
		t.Fatal(err)
	}
	b := Bundle{SpeakAs: kit.SpeakAs, NameMap: []NameEntry{{Member: kit.Member, Location: &loc}}}
	raw, err := EncodeBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.NameMap) != 1 || got.NameMap[0].Member.Aud != kit.Member.Aud ||
		got.NameMap[0].Location == nil || !slices.Equal(got.NameMap[0].Location.Cav.Endpoints, loc.Cav.Endpoints) {
		t.Fatalf("round trip: %+v", got.NameMap)
	}

	// Tampered: a renamed member cert, a location signed by a different
	// key than the member, and a grant where a member cert should be —
	// none decode.
	bad := kit.Member
	bad.Cav.Name = "cp1"
	if raw, _ = EncodeBundle(Bundle{SpeakAs: kit.SpeakAs, NameMap: []NameEntry{{Member: bad}}}); raw != nil {
		if _, err := DecodeBundle(raw); err == nil {
			t.Fatal("renamed member decoded")
		}
	}
	other := newNode(t)
	otherLoc, _ := cert.Sign(cert.Cert{Aud: cert.AudAny, Can: cert.VerbReachMeAt, Cav: cert.Caveats{Endpoints: []string{"x"}}, Iat: now, Exp: now + 3600}, other)
	if raw, _ = EncodeBundle(Bundle{SpeakAs: kit.SpeakAs, NameMap: []NameEntry{{Member: kit.Member, Location: &otherLoc}}}); raw != nil {
		if _, err := DecodeBundle(raw); err == nil {
			t.Fatal("location from another key decoded as the member's")
		}
	}
	if raw, _ = EncodeBundle(Bundle{SpeakAs: kit.SpeakAs, NameMap: []NameEntry{{Member: kit.BeatGrant}}}); raw != nil {
		if _, err := DecodeBundle(raw); err == nil {
			t.Fatal("grant decoded as a name-map member")
		}
	}
}

// ---- 3. end to end: locations ride the beat --------------------------------

// TestNameMapCarriesPiggybackedLocations: over the wire, cp1's envelope
// piggybacks its reach-me-at into the hub's location cache; laptop's
// next #bundle hands it back under cp1's name — the hub relayed a
// record cp1 issued, and laptop can dial cp1 by name with nothing but
// the bundle. A member whose record has expired is still in the map,
// without a location.
func TestNameMapCarriesPiggybackedLocations(t *testing.T) {
	clk := newClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	net := actor.NewMemoryNetwork()
	w := newWallet(t)

	_, privH, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	epH, err := net.Bind(cert.NewEdSigner(privH).ActorID(), "hub")
	if err != nil {
		t.Fatal(err)
	}
	iss := NewWithKey(privH, testGroups, epH, clk.Now)
	iss.Policy = filePolicy(t, testRecipe)
	w.unseal(t, iss)
	H := iss.ID()

	listen := func(ac *actor.Actor) {
		done := make(chan error, 1)
		go func() { done <- ac.Listen(ctx) }()
		t.Cleanup(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("Listen: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Listen did not stop")
			}
		})
	}
	listen(iss.Actor)

	member := func(name, group string) (*actor.Actor, Kit) {
		s := newNode(t)
		ep, err := net.Bind(s.ActorID(), name)
		if err != nil {
			t.Fatal(err)
		}
		kit, err := iss.Mint(s.ActorID(), name, []string{group})
		if err != nil {
			t.Fatal(err)
		}
		a := actor.New(s, ep)
		a.Clock = clk.Now
		a.Grant(H, FacetBundle, kit.BeatGrant, kit.SpeakAs)
		listen(a)
		return a, kit
	}
	cp1, cp1kit := member("cp1", "machines")
	laptop, laptopKit := member("laptop", "admins")

	// cp1 publishes a 1 h record and beats: the envelope carries it.
	if _, err := cp1.PublishLocation(3600); err != nil {
		t.Fatal(err)
	}
	if _, err := cp1.Send(ctx, H, FacetBundle, bundleReq(t, cp1kit.Member)); err != nil {
		t.Fatalf("cp1 beat: %v", err)
	}

	// laptop beats without a record of its own and receives cp1's.
	rep, err := laptop.Send(ctx, H, FacetBundle, bundleReq(t, laptopKit.Member))
	if err != nil {
		t.Fatalf("laptop beat: %v", err)
	}
	b, err := DecodeBundle(rep.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(b.NameMap); !slices.Equal(got, []string{"cp1", "laptop"}) {
		t.Fatalf("map = %v", got)
	}
	e := Lookup(b.NameMap, "cp1")[0]
	if e.Location == nil || e.Location.Iss != cp1.ID() || len(e.Location.Cav.Endpoints) == 0 {
		t.Fatalf("cp1's location missing or not its own: %+v", e.Location)
	}
	if Lookup(b.NameMap, "laptop")[0].Location != nil {
		t.Fatal("laptop never published, yet the map locates it")
	}
	// The record is exactly what cp1 put on the wire — the hub relayed,
	// it did not re-issue.
	if own := cp1.CurrentLocation(); own == nil || !slices.Equal(own.Sig, e.Location.Sig) {
		t.Fatal("hub handed out a location cp1 did not sign")
	}

	// After the record lapses cp1 is still named, just not located.
	clk.Advance(3601)
	rep, err = laptop.Send(ctx, H, FacetBundle, bundleReq(t, laptopKit.Member))
	if err != nil {
		t.Fatal(err)
	}
	if b, err = DecodeBundle(rep.Payload); err != nil {
		t.Fatal(err)
	}
	if got := Lookup(b.NameMap, "cp1"); len(got) != 1 || got[0].Location != nil {
		t.Fatalf("expired location still shipped: %+v", got)
	}
}
