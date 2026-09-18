package policy

import (
	"crypto/ed25519"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"github.com/marnyg/talos-config/protocol/cert"
)

// The round-trip law (talos-config-4un). Nickel checks the recipe and
// Quint the verifier; this checks the step between:
//
//	∀ recipe R (closed vocab), caller I, kind K, facet F, now:
//	  R.Allows(I, K, F)
//	    ⇔ Authorize(receiver of kind K {consent → W over Facets(K),
//	                 AcceptTable(K)},
//	                bundle{member(I) by W, Compile(R, I, now) signed by W},
//	                ALPN(F), now).OK
//
// ⇐ is the load-bearing direction: the compiler emits nothing the
// recipe did not ask for. A mis-implemented target (say, a literal
// receiver id or an empty set) or a facet leak across kinds would show
// up here as an accept the reference interpreter denies, or a deny it
// allows. Negatives — F of another kind (ALPN miss), a blocklisted
// peer, now ≥ exp — reject regardless of the recipe.
func TestCompileRoundTrip(t *testing.T) {
	names := []string{"laptop", "phone", "tv", "cp1", "hub"}
	all := allFacets()

	genRule := func(k Kind) *rapid.Generator[Rule] {
		return rapid.Custom(func(t *rapid.T) Rule {
			r := Rule{Facet: rapid.SampledFrom(Facets(k)).Draw(t, "facet")}
			if rapid.Bool().Draw(t, "byHost") {
				r.Host = rapid.SampledFrom(names).Draw(t, "host")
			} else {
				gs := Groups
				if k == KindNode {
					gs = []string{"admins", "media"} // NodeIsolatesMachines
				}
				r.Group = rapid.SampledFrom(gs).Draw(t, "group")
			}
			return r
		})
	}
	genRecipe := rapid.Custom(func(t *rapid.T) Recipe {
		return Recipe{
			Node:    Scope{Inbound: rapid.SliceOfN(genRule(KindNode), 0, 4).Draw(t, "node")},
			Gateway: Scope{Inbound: rapid.SliceOfN(genRule(KindGateway), 0, 4).Draw(t, "gateway")},
			Hub:     Scope{Inbound: rapid.SliceOfN(genRule(KindHub), 0, 3).Draw(t, "hub")},
		}
	})

	rapid.Check(t, func(t *rapid.T) {
		recipe := genRecipe.Draw(t, "recipe")
		if err := recipe.Validate(); err != nil {
			t.Fatalf("generator produced an invalid recipe: %v", err)
		}

		// Three keys: the Owner W (sovereign; signs member + grants), the
		// caller C (QUIC peer, member aud) and the receiver R.
		w, c, r := edSigner(1), edSigner(2), edSigner(3)
		caller := Caller{
			Key:    c.ActorID(),
			Name:   rapid.SampledFrom(names).Draw(t, "name"),
			Groups: slices.Sorted(slices.Values(rapid.SliceOfNDistinct(rapid.SampledFrom(Groups), 0, 3, rapid.ID[string]).Draw(t, "groups"))),
		}
		kind := rapid.SampledFrom(Kinds).Draw(t, "kind")
		facet := rapid.SampledFrom(all).Draw(t, "facet")
		const iat int64 = 1_700_000_000
		now := iat + rapid.Int64Range(0, GrantTTL+3600).Draw(t, "now-iat")
		blocked := rapid.Bool().Draw(t, "blocked")

		consent := mustSign(t, cert.Cert{
			Aud: string(w.ActorID()), Can: cert.VerbInvoke,
			Cav: cert.Caveats{Target: []cert.ActorID{r.ActorID()}, Facet: Facets(kind), Delegable: true},
			Iat: iat, Exp: iat + 100*GrantTTL,
		}, r)
		member := mustSign(t, cert.Cert{
			Aud: string(c.ActorID()), Can: cert.VerbMember,
			Cav: cert.Caveats{Name: caller.Name, Groups: caller.Groups},
			Iat: iat, Exp: iat + 100*GrantTTL,
		}, w)
		var grants []cert.Cert
		for _, g := range Compile(recipe, caller, iat) {
			grants = append(grants, mustSign(t, g, w))
		}

		in := cert.Input{
			Receiver:    cert.Receiver{ID: r.ActorID(), Consents: []cert.Cert{consent}},
			AcceptTable: AcceptTable(kind),
			Blocklist:   map[cert.ActorID]bool{},
			Now:         now,
			ALPN:        ALPN(facet),
			Peer:        c.ActorID(),
			Bundle:      cert.Bundle{Member: member, Grants: grants},
		}
		if blocked {
			in.Blocklist[c.ActorID()] = true
		}
		got := cert.Authorize(in).OK
		want := recipe.Allows(caller, kind, facet) && !blocked && now < iat+GrantTTL
		if got != want {
			t.Fatalf("round-trip broken: Authorize=%v, Allows∧live∧¬blocked=%v\nkind=%s facet=%s caller=%+v now-iat=%d blocked=%v\nrecipe=%+v\ngrants=%+v",
				got, want, kind, facet, caller, now-iat, blocked, recipe, grants)
		}
	})
}

// Compile is deterministic and emits exactly one grant per matching
// row, each carrying only that row's facet and the wildcard target.
func TestCompileShapeLaws(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rows := rapid.SliceOfN(rapid.Custom(func(t *rapid.T) Rule {
			k := rapid.SampledFrom(Kinds).Draw(t, "k")
			r := Rule{Facet: rapid.SampledFrom(Facets(k)).Draw(t, "f")}
			if rapid.Bool().Draw(t, "h") {
				r.Host = rapid.SampledFrom([]string{"a", "b"}).Draw(t, "host")
			} else {
				r.Group = rapid.SampledFrom(Groups).Draw(t, "g")
			}
			return r
		}), 0, 6).Draw(t, "rows")
		var recipe Recipe
		for _, r := range rows {
			switch KindOf(r.Facet) {
			case KindNode:
				recipe.Node.Inbound = append(recipe.Node.Inbound, r)
			case KindGateway:
				recipe.Gateway.Inbound = append(recipe.Gateway.Inbound, r)
			case KindHub:
				recipe.Hub.Inbound = append(recipe.Hub.Inbound, r)
			}
		}
		caller := Caller{Key: edSigner(2).ActorID(), Name: rapid.SampledFrom([]string{"a", "b", "c"}).Draw(t, "name"),
			Groups: rapid.SliceOfNDistinct(rapid.SampledFrom(Groups), 0, 3, rapid.ID[string]).Draw(t, "groups")}
		now := rapid.Int64Range(0, 1<<40).Draw(t, "now")

		a, b := Compile(recipe, caller, now), Compile(recipe, caller, now)
		if !slices.EqualFunc(a, b, func(x, y cert.Cert) bool { return x.Aud == y.Aud && slices.Equal(x.Cav.Facet, y.Cav.Facet) }) {
			t.Fatal("Compile is not deterministic")
		}
		matching := 0
		for _, k := range Kinds {
			for _, r := range recipe.Scope(k) {
				if r.Host == caller.Name || slices.Contains(caller.Groups, r.Group) {
					matching++
				}
			}
		}
		if len(a) != matching {
			t.Fatalf("%d grants for %d matching rows", len(a), matching)
		}
		for _, g := range a {
			if len(g.Cav.Facet) != 1 || !cert.IsTargetAny(g.Cav.Target) || g.Can != cert.VerbInvoke || g.Cav.Delegable || g.Exp-g.Iat != GrantTTL {
				t.Fatalf("grant shape: %+v", g)
			}
			if g.Aud != string(caller.Key) && !slices.Contains(caller.Groups, g.Aud[len("group:"):]) {
				t.Fatalf("grant addressed to someone else: %+v", g)
			}
		}
	})
}

func allFacets() []string {
	var out []string
	for _, k := range Kinds {
		out = append(out, Facets(k)...)
	}
	return out
}

// edSigner is a deterministic, distinct-per-tag key: the law is about
// the compiler, not key generation, and fixed keys keep failures readable.
func edSigner(tag byte) cert.EdSigner {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = tag
	}
	return cert.NewEdSigner(ed25519.NewKeyFromSeed(seed))
}

func mustSign(t *rapid.T, c cert.Cert, s cert.Signer) cert.Cert {
	signed, err := cert.Sign(c, s)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}
