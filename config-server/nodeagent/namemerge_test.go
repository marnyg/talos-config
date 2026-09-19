package nodeagent

import (
	"testing"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/cert"
)

// entry is a name-map entry for a fake NodeId; certs are not signed
// here (mergeNameMaps is the pure projection, verification happened at
// decode).
func entry(id, name string, exp int64, locExp int64) issuer.NameEntry {
	e := issuer.NameEntry{Member: cert.Cert{Aud: id, Can: cert.VerbMember, Cav: cert.Caveats{Name: name}, Exp: exp}}
	if locExp > 0 {
		e.Location = &cert.Cert{Iss: cert.ActorID(id), Can: cert.VerbReachMeAt, Exp: locExp}
	}
	return e
}

func names(entries []issuer.NameEntry) map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Member.Cav.Name] = true
	}
	return out
}

// A member that beats a freshly deployed hub sees only itself in the
// witness cache; its own last copy carries the peers over.
func TestMergeNameMapsKeepsUnwitnessedPeers(t *testing.T) {
	const now = 1000
	prev := []issuer.NameEntry{entry("ed:aa", "cp1", 2000, 2000), entry("ed:bb", "laptop", 2000, 2000)}
	fresh := issuer.Bundle{NameMap: []issuer.NameEntry{entry("ed:bb", "laptop", 3000, 3000)}}

	got := mergeNameMaps(prev, fresh, now)
	if n := names(got); !n["cp1"] || !n["laptop"] || len(got) != 2 {
		t.Fatalf("merged %v", n)
	}
	// The hub's entry wins for a NodeId it knows.
	for _, e := range got {
		if e.Member.Aud == "ed:bb" && e.Member.Exp != 3000 {
			t.Fatalf("stale entry shadowed the hub's: %+v", e)
		}
	}
}

// Kept entries are still subject to expiry and to the blocklist the hub
// just sent (j0b); an expired location drops but the name stays.
func TestMergeNameMapsDropsDeadAndBlocked(t *testing.T) {
	const now = 1000
	prev := []issuer.NameEntry{
		entry("ed:aa", "expired", 900, 2000),
		entry("ed:bb", "blocked", 2000, 2000),
		entry("ed:cc", "stale-loc", 2000, 900),
	}
	fresh := issuer.Bundle{Blocklist: []cert.ActorID{"ed:bb"}}

	got := mergeNameMaps(prev, fresh, now)
	n := names(got)
	if n["expired"] || n["blocked"] || !n["stale-loc"] || len(got) != 1 {
		t.Fatalf("merged %v", n)
	}
	if got[0].Location != nil {
		t.Fatalf("expired location kept: %+v", got[0].Location)
	}
}

// No previous bundle, or a hub that knows everything: fresh passes through.
func TestMergeNameMapsIdentity(t *testing.T) {
	fresh := issuer.Bundle{NameMap: []issuer.NameEntry{entry("ed:aa", "cp1", 2000, 2000)}}
	if got := mergeNameMaps(nil, fresh, 1000); len(got) != 1 {
		t.Fatalf("merged %d entries", len(got))
	}
	if got := mergeNameMaps(fresh.NameMap, fresh, 1000); len(got) != 1 {
		t.Fatalf("merged %d entries", len(got))
	}
}
