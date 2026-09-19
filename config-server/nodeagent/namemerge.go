package nodeagent

// Name-map reconvergence (talos-config-359.8.6, exit check "hub
// re-seal"): the hub's witness cache is empty after every deploy and
// fills only as members beat (decision 2fc). A member that beats the
// fresh hub first would see a name map without its peers and lose them
// for up to one beat interval — so it keeps its own last copy for the
// entries the hub has not re-witnessed yet.
//
// This widens the map; it never weakens what proves an entry. Every
// entry (kept or fresh) is an Owner-signed member cert verified at
// decode, the receiver still authorizes on the bundle presented to it,
// and a kept entry is dropped the moment it expires or lands on the
// blocklist the hub just sent (j0b: the fresh blocklist governs).

import (
	"slices"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/protocol/cert"
)

// mergeNameMaps returns fresh.NameMap plus the still-usable entries of
// prev the fresh map does not mention. The hub's entry always wins for
// a NodeId it knows. A kept entry whose location has expired is kept
// without one (the name still resolves; it is simply not dialable).
func mergeNameMaps(prev []issuer.NameEntry, fresh issuer.Bundle, now int64) []issuer.NameEntry {
	known := make(map[cert.ActorID]bool, len(fresh.NameMap))
	for _, e := range fresh.NameMap {
		known[cert.ActorID(e.Member.Aud)] = true
	}
	blocked := make(map[cert.ActorID]bool, len(fresh.Blocklist))
	for _, id := range fresh.Blocklist {
		blocked[id] = true
	}
	out := slices.Clone(fresh.NameMap)
	for _, e := range prev {
		id := cert.ActorID(e.Member.Aud)
		if known[id] || blocked[id] || e.Member.Exp <= now {
			continue
		}
		if e.Location != nil && e.Location.Exp <= now {
			e.Location = nil
		}
		out = append(out, e)
	}
	return out
}
