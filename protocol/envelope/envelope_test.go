package envelope

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"testing"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/marnyg/talos-config/protocol/cert"
)

// ---- fixtures -------------------------------------------------------------

func edSigner(t testing.TB) cert.EdSigner {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return cert.NewEdSigner(priv)
}

func ethSigner(t testing.TB) cert.EthSigner {
	t.Helper()
	priv, err := secp.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return cert.NewEthSigner(priv)
}

const now int64 = 1_000_000

func mustSign(t testing.TB, c cert.Cert, s cert.Signer) cert.Cert {
	t.Helper()
	out, err := cert.Sign(c, s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// consent: receiver → principal, delegable, targeting receiver, facet f.
func consent(t testing.TB, receiver cert.Signer, principal cert.ActorID, facet string) cert.Cert {
	t.Helper()
	return mustSign(t, cert.Cert{
		Aud: string(principal), Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{receiver.ActorID()}, Facet: []string{facet}, Delegable: true},
		Iat: now - 100, Exp: now + 3600,
	}, receiver)
}

// grant: principal → aud, invoking receiver on facet f.
func grant(t testing.TB, principal cert.Signer, aud, receiver cert.ActorID, facet string) cert.Cert {
	t.Helper()
	return mustSign(t, cert.Cert{
		Aud: string(aud), Can: cert.VerbInvoke,
		Cav: cert.Caveats{Target: []cert.ActorID{receiver}, Facet: []string{facet}},
		Iat: now - 50, Exp: now + 1800,
	}, principal)
}

func reachMeAt(t testing.TB, s cert.Signer, exp int64) *cert.Cert {
	t.Helper()
	c := mustSign(t, cert.Cert{
		Aud: "*", Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Verbs: []string{"mem:" + string(s.ActorID())}},
		Iat: now - 10, Exp: exp,
	}, s)
	return &c
}

// stubChain is a small, honest ChainVerifier for tests: it follows the
// shape of the errata VerifyChain contract (consent lookup by the first
// link's signer, Attenuate fold, target/facet/expiry, aud == signer on
// the last link) without speak-as or group resolution. It records its
// calls so tests can assert cost order and argument plumbing. Like
// cert.VerifyChain it returns the rooted certs it verified so far even
// when it rejects — the receiver-signed root and every link whose
// signature checked — so Verify's "Verified alongside ErrChain" contract
// is exercised by the fake, not just by cert.VerifyChain.
type stubChain struct {
	mu    sync.Mutex
	calls []stubCall
}

type stubCall struct {
	receiver cert.ActorID
	chain    []cert.Cert
	speakAs  []cert.Cert
	signer   cert.ActorID
	facet    string
	now      int64
}

func (s *stubChain) verify(r cert.Receiver, chain, speakAs []cert.Cert, signer cert.ActorID, facet string, now int64) (cert.Cert, []cert.Cert, error) {
	receiver, consents := r.ID, r.Consents
	s.mu.Lock()
	s.calls = append(s.calls, stubCall{receiver, chain, speakAs, signer, facet, now})
	s.mu.Unlock()
	if len(chain) == 0 {
		return cert.Cert{}, nil, errors.New("empty chain")
	}
	var root *cert.Cert
	for i := range consents {
		c := consents[i]
		if c.Iss == receiver && c.Aud == string(chain[0].Iss) && cert.Verify(c) == nil {
			root = &c
			break
		}
	}
	if root == nil {
		return cert.Cert{}, nil, errors.New("chain not rooted at receiver")
	}
	verified := []cert.Cert{*root}
	eff := *root
	for _, link := range chain {
		if cert.Verify(link) != nil {
			return cert.Cert{}, verified, errors.New("link sig")
		}
		verified = append(verified, link)
		var err error
		if eff, err = cert.Attenuate(eff, link); err != nil {
			return cert.Cert{}, verified, err
		}
	}
	switch {
	case eff.Exp <= now:
		return cert.Cert{}, verified, errors.New("expired")
	case !containsID(eff.Cav.Target, receiver):
		return cert.Cert{}, verified, errors.New("target")
	case !containsStr(eff.Cav.Facet, facet):
		return cert.Cert{}, verified, errors.New("facet")
	case chain[len(chain)-1].Aud != string(signer):
		return cert.Cert{}, verified, errors.New("aud != signer")
	}
	return eff, verified, nil
}

func containsID(xs []cert.ActorID, x cert.ActorID) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// world is the two-party fixture: receiver B holds a consent for
// principal P; caller A carries a grant P→A. Facet "api".
type world struct {
	a, b, p cert.Signer
	recv    Receiver
	chain   *stubChain
	proof   []cert.Cert
}

func newWorld(t testing.TB, a, b, p cert.Signer) *world {
	t.Helper()
	sc := &stubChain{}
	w := &world{a: a, b: b, p: p, chain: sc}
	w.recv = Receiver{
		ID:       b.ActorID(),
		Consents: []cert.Cert{consent(t, b, p.ActorID(), "api")},
		Chain:    sc.verify,
		HWM:      NewHWM(),
	}
	w.proof = []cert.Cert{grant(t, p, a.ActorID(), b.ActorID(), "api")}
	return w
}

func (w *world) envelope(t testing.TB, seq int64, payload string) Envelope {
	t.Helper()
	e, err := Sign(Envelope{
		To:      Address{Target: w.b.ActorID(), Facet: "api"},
		Seq:     seq,
		Payload: []byte(payload),
		Proof:   w.proof,
	}, w.a)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// ---- tests ----------------------------------------------------------------

func TestEnvelopeSignVerify(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func(testing.TB) cert.Signer
	}{
		{"ed25519", func(t testing.TB) cert.Signer { return edSigner(t) }},
		{"eth", func(t testing.TB) cert.Signer { return ethSigner(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, tc.mk(t), edSigner(t), tc.mk(t))
			e := w.envelope(t, 1, "hello")
			if e.From != w.a.ActorID() {
				t.Fatalf("Sign must set From; got %s", e.From)
			}
			res, err := Verify(e, w.recv, now)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if !containsID(res.Eff.Cav.Target, w.b.ActorID()) || len(res.Verified) != 2 {
				t.Fatalf("unexpected result %+v", res)
			}

			// Every signed field is covered by the signature.
			mut := []struct {
				name string
				f    func(*Envelope)
			}{
				{"payload", func(e *Envelope) { e.Payload = []byte("hellO") }},
				{"seq", func(e *Envelope) { e.Seq++ }},
				{"facet", func(e *Envelope) { e.To.Facet = "admin" }},
				{"target", func(e *Envelope) { e.To.Target = w.a.ActorID() }},
				{"proof", func(e *Envelope) { e.Proof = nil }},
				{"loc", func(e *Envelope) { e.Loc = reachMeAt(t, w.a, now+60) }},
				{"from", func(e *Envelope) { e.From = w.p.ActorID() }},
				{"sig", func(e *Envelope) { e.Sig = append([]byte{}, e.Sig[1:]...) }},
			}
			for _, m := range mut {
				fresh := NewHWM()
				recv := w.recv
				recv.HWM = fresh
				x := e
				x.Proof = append([]cert.Cert{}, e.Proof...)
				m.f(&x)
				if _, err := Verify(x, recv, now); !errors.Is(err, ErrSig) {
					t.Errorf("mutating %s: want ErrSig, got %v", m.name, err)
				}
			}
		})
	}
}

func TestVerifyCostOrder(t *testing.T) {
	w := newWorld(t, edSigner(t), edSigner(t), edSigner(t))

	// Wrong target: signature is fine, chain must not be consulted.
	stranger := edSigner(t)
	e, err := Sign(Envelope{To: Address{Target: stranger.ActorID(), Facet: "api"}, Seq: 1, Proof: w.proof}, w.a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(e, w.recv, now); !errors.Is(err, ErrWrongTarget) {
		t.Fatalf("want ErrWrongTarget, got %v", err)
	}
	if len(w.chain.calls) != 0 {
		t.Fatalf("chain consulted before target check")
	}
	if got := w.recv.HWM.Peek(w.a.ActorID(), stranger.ActorID()); got != 0 {
		t.Fatalf("hwm advanced on wrong-target envelope: %d", got)
	}

	// Bad chain (unrooted signer): sig/target/seq pass, chain rejects,
	// and the seq is still spent.
	outsider := edSigner(t)
	bad, err := Sign(Envelope{To: Address{Target: w.b.ActorID(), Facet: "api"}, Seq: 7, Proof: w.proof}, outsider)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(bad, w.recv, now); !errors.Is(err, ErrChain) {
		t.Fatalf("want ErrChain, got %v", err)
	}
	if got := w.recv.HWM.Peek(outsider.ActorID(), w.b.ActorID()); got != 7 {
		t.Fatalf("hwm not advanced past sig+target: %d", got)
	}

	// Chain plumbing: signer = From, facet = To.Facet, speak-as split out.
	sa := mustSign(t, cert.Cert{
		Aud: string(w.a.ActorID()), Can: cert.VerbSpeakAs,
		Cav: cert.Caveats{Verbs: []string{"invoke"}}, Iat: now - 1, Exp: now + 100,
	}, w.p)
	e = w.envelope(t, 1, "x")
	e.Proof = []cert.Cert{sa, w.proof[0]}
	e, _ = Sign(e, w.a)
	if _, err := Verify(e, w.recv, now); err != nil {
		t.Fatal(err)
	}
	last := w.chain.calls[len(w.chain.calls)-1]
	if last.signer != w.a.ActorID() || last.facet != "api" || last.receiver != w.b.ActorID() || last.now != now {
		t.Fatalf("chain args: %+v", last)
	}
	if len(last.chain) != 1 || len(last.speakAs) != 1 || last.speakAs[0].Can != cert.VerbSpeakAs {
		t.Fatalf("proof not split by verb: chain=%d speakAs=%d", len(last.chain), len(last.speakAs))
	}

	// No chain rule ⇒ fail closed.
	recv := w.recv
	recv.Chain = nil
	recv.HWM = NewHWM()
	if res, err := Verify(w.envelope(t, 1, "x"), recv, now); !errors.Is(err, ErrNoChainVerifier) {
		t.Fatalf("want ErrNoChainVerifier, got %v", err)
	} else if len(res.Verified) != 0 {
		t.Fatalf("cheap reject must verify nothing rooted at r: %+v", res)
	}
}

// A chain rejection still hands back the certs the rule verified on a
// chain rooted at the receiver: the low-water mark advances on accept
// and reject alike (invariant 9, ADR-0019).
func TestVerifyReturnsVerifiedOnChainReject(t *testing.T) {
	w := newWorld(t, edSigner(t), edSigner(t), edSigner(t))
	root := w.recv.Consents[0]

	recv := w.recv
	recv.Chain = func(_ cert.Receiver, chain, speakAs []cert.Cert, signer cert.ActorID, facet string, _ int64) (cert.Cert, []cert.Cert, error) {
		// Rooted, signature-checked — then rejected on a later rule
		// (expiry, caveat, aud binding). cert.VerifyChain does the same.
		return cert.Cert{}, []cert.Cert{root}, errors.New("forced reject after root")
	}

	res, err := Verify(w.envelope(t, 1, "x"), recv, now)
	if !errors.Is(err, ErrChain) {
		t.Fatalf("want ErrChain, got %v", err)
	}
	if len(res.Verified) != 1 || res.Verified[0].Iat != root.Iat {
		t.Fatalf("verified dropped on chain reject: %+v", res.Verified)
	}
}

func TestReplyBindsToRequest(t *testing.T) {
	w := newWorld(t, edSigner(t), ethSigner(t), edSigner(t))
	req := w.envelope(t, 1, "ping")
	if _, err := Verify(req, w.recv, now); err != nil {
		t.Fatal(err)
	}

	rep, err := NewReply(req, []byte("pong"), nil)
	if err != nil {
		t.Fatal(err)
	}
	rep, err = SignReply(rep, w.b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReply(rep, req, now); err != nil {
		t.Fatalf("verify reply: %v", err)
	}
	h, _ := Hash(req)
	if !bytes.Equal(rep.Re, h) {
		t.Fatal("re != Hash(req)")
	}

	// Bound to a different request (same content, different seq).
	other := w.envelope(t, 2, "ping")
	if _, err := VerifyReply(rep, other, now); !errors.Is(err, ErrReplyMismatch) {
		t.Fatalf("want ErrReplyMismatch, got %v", err)
	}
	// Signed by someone other than the request's target.
	imp, _ := SignReply(Reply{Re: rep.Re, Payload: rep.Payload}, w.p)
	if _, err := VerifyReply(imp, req, now); !errors.Is(err, ErrReplyFrom) {
		t.Fatalf("want ErrReplyFrom, got %v", err)
	}
	// Payload tamper.
	tam := rep
	tam.Payload = []byte("pwned")
	if _, err := VerifyReply(tam, req, now); !errors.Is(err, ErrSig) {
		t.Fatalf("want ErrSig, got %v", err)
	}
}

func TestSequenceValidation(t *testing.T) {
	w := newWorld(t, edSigner(t), edSigner(t), edSigner(t))
	a, b := w.a.ActorID(), w.b.ActorID()

	for _, seq := range []int64{1, 2, 3} {
		if _, err := Verify(w.envelope(t, seq, "m"), w.recv, now); err != nil {
			t.Fatalf("seq %d: %v", seq, err)
		}
	}
	if got := w.recv.HWM.Peek(a, b); got != 3 {
		t.Fatalf("hwm = %d, want 3", got)
	}
	// Replay of an already-accepted envelope, and any seq ≤ mark.
	replay := w.envelope(t, 2, "m")
	if _, err := Verify(replay, w.recv, now); !errors.Is(err, ErrReplay) {
		t.Fatalf("want ErrReplay, got %v", err)
	}
	if _, err := Verify(w.envelope(t, 3, "m"), w.recv, now); !errors.Is(err, ErrReplay) {
		t.Fatalf("seq == hwm must reject")
	}
	if _, err := Verify(w.envelope(t, 0, "m"), w.recv, now); !errors.Is(err, ErrReplay) {
		t.Fatalf("seq 0 must reject")
	}
	// Gaps are fine (at-most-once, best-effort): 4 → 10.
	if _, err := Verify(w.envelope(t, 10, "m"), w.recv, now); err != nil {
		t.Fatal(err)
	}
	if got := w.recv.HWM.Peek(a, b); got != 10 {
		t.Fatalf("hwm = %d, want 10", got)
	}

	// Marks are per (sender, receiver): another sender and another
	// receiver each start from 0.
	var h HWM // zero value usable
	c := cert.ActorID("ed:" + "00")
	if !h.Check(a, b, 5) || h.Check(a, b, 5) || !h.Check(c, b, 1) || !h.Check(a, c, 1) {
		t.Fatal("marks not independent per (from, to)")
	}
	if h.Peek(a, b) != 5 || h.Peek(c, b) != 1 || h.Peek(a, c) != 1 || h.Peek(c, c) != 0 {
		t.Fatal("peek")
	}
}

func TestHWMConcurrent(t *testing.T) {
	h := NewHWM()
	a, b := cert.ActorID("ed:a"), cert.ActorID("ed:b")
	var wg sync.WaitGroup
	accepted := make(chan int64, 1000)
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := int64(1); i <= 100; i++ {
				if h.Check(a, b, i) {
					accepted <- i
				}
			}
		}(g)
	}
	wg.Wait()
	close(accepted)
	seen := map[int64]bool{}
	for s := range accepted {
		if seen[s] {
			t.Fatalf("seq %d accepted twice", s)
		}
		seen[s] = true
	}
	if h.Peek(a, b) != 100 {
		t.Fatalf("hwm = %d", h.Peek(a, b))
	}
}

func TestLocationCaching(t *testing.T) {
	w := newWorld(t, edSigner(t), edSigner(t), edSigner(t))

	// Good loc: verify succeeds and the record is handed back.
	good := reachMeAt(t, w.a, now+3600)
	e := w.envelope(t, 1, "x")
	e.Loc = good
	e, _ = Sign(e, w.a)
	res, err := Verify(e, w.recv, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Loc == nil || !bytes.Equal(res.Loc.Sig, good.Sig) {
		t.Fatal("good loc not returned")
	}
	// Absent loc: nil, no error.
	if res, err := Verify(w.envelope(t, 2, "x"), w.recv, now); err != nil || res.Loc != nil {
		t.Fatalf("absent loc: %v %v", res.Loc, err)
	}

	// Each bad loc rejects the whole envelope with ErrBadLoc.
	tampered := *good
	tampered.Cav.Verbs = []string{"mem:elsewhere"}
	wrongVerb := mustSign(t, cert.Cert{Aud: "*", Can: cert.VerbPublish, Iat: now, Exp: now + 60}, w.a)
	notMine := reachMeAt(t, w.p, now+60)
	bad := map[string]*cert.Cert{
		"expired":         reachMeAt(t, w.a, now),
		"tampered":        &tampered,
		"wrong verb":      &wrongVerb,
		"issued by other": notMine,
	}
	seq := int64(10)
	for name, loc := range bad {
		seq++
		e := w.envelope(t, seq, "x")
		e.Loc = loc
		e, _ = Sign(e, w.a)
		if _, err := Verify(e, w.recv, now); !errors.Is(err, ErrBadLoc) {
			t.Errorf("%s: want ErrBadLoc, got %v", name, err)
		}
	}

	// Same rule on the reply path.
	req := w.envelope(t, 100, "ping")
	if _, err := Verify(req, w.recv, now); err != nil {
		t.Fatal(err)
	}
	rep, _ := NewReply(req, []byte("pong"), reachMeAt(t, w.b, now+60))
	rep, _ = SignReply(rep, w.b)
	loc, err := VerifyReply(rep, req, now)
	if err != nil || loc == nil {
		t.Fatalf("reply loc: %v %v", loc, err)
	}
	rep, _ = NewReply(req, []byte("pong"), reachMeAt(t, w.b, now-1))
	rep, _ = SignReply(rep, w.b)
	if _, err := VerifyReply(rep, req, now); !errors.Is(err, ErrBadLoc) {
		t.Fatalf("reply expired loc: want ErrBadLoc, got %v", err)
	}
}

func TestWireRoundTrip(t *testing.T) {
	w := newWorld(t, ethSigner(t), edSigner(t), ethSigner(t))
	e := w.envelope(t, 3, "payload bytes \x00\xff")
	// A loc whose aud is an actor id: checkLoc ignores loc.Aud, so this
	// exercises the non-wildcard path; the "*" (cert.AudAny) round trip
	// is covered in cert_test.go.
	loc := mustSign(t, cert.Cert{
		Aud: string(w.b.ActorID()), Can: cert.VerbReachMeAt,
		Cav: cert.Caveats{Verbs: []string{"mem:a"}}, Iat: now, Exp: now + 60,
	}, w.a)
	e.Loc = &loc
	e, _ = Sign(e, w.a)

	wire, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(wire)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, wire)
	}
	h1, _ := Hash(e)
	h2, _ := Hash(back)
	if !bytes.Equal(h1, h2) {
		t.Fatal("hash changed across the wire")
	}
	if _, err := Verify(back, w.recv, now); err != nil {
		t.Fatalf("verify decoded: %v", err)
	}
	// Unknown key rejects.
	if _, err := Decode([]byte(`{"from":"ed:00","x":1}`)); err == nil {
		t.Fatal("unknown key accepted")
	}

	rep, _ := NewReply(e, []byte("ok"), nil)
	rep, _ = SignReply(rep, w.b)
	rw, err := EncodeReply(rep)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := DecodeReply(rw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyReply(rb, e, now); err != nil {
		t.Fatal(err)
	}
}

// Example_handshake: A invokes B twice (seq 1, 2) under a one-link
// chain P→A rooted at B's consent for P; B replies once to the second.
func Example_handshake() {
	t := &testing.T{}
	w := newWorld(t, edSigner(t), edSigner(t), edSigner(t))

	for seq, msg := range []string{"hello", "how are you?"} {
		e := w.envelope(t, int64(seq+1), msg)
		res, err := Verify(e, w.recv, now)
		fmt.Printf("seq %d: err=%v verified=%d\n", e.Seq, err, len(res.Verified))
	}
	// A replay of seq 2 is refused before the chain is consulted.
	before := len(w.chain.calls)
	_, err := Verify(w.envelope(t, 2, "how are you?"), w.recv, now)
	fmt.Printf("replay: %v (chain calls +%d)\n", errors.Is(err, ErrReplay), len(w.chain.calls)-before)

	req := w.envelope(t, 3, "ping")
	_, _ = Verify(req, w.recv, now)
	rep, _ := NewReply(req, []byte("pong"), nil)
	rep, _ = SignReply(rep, w.b)
	_, err = VerifyReply(rep, req, now)
	fmt.Printf("reply ok=%v hwm=%d\n", err == nil, w.recv.HWM.Peek(w.a.ActorID(), w.b.ActorID()))
	// Output:
	// seq 1: err=<nil> verified=2
	// seq 2: err=<nil> verified=2
	// replay: true (chain calls +0)
	// reply ok=true hwm=3
}
