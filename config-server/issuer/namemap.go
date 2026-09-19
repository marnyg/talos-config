package issuer

// The name map: the directory half of #bundle (glossary "Name map";
// decision talos-config-2fc). Two halves, two owners, neither minted
// here:
//
//   - name → NodeId is WITNESSED, not registered. Git holds names
//     (talos/machines/<mac>/meta.yaml, the approver-set device name);
//     keys are minted on members (ADR-0015) and the hub keeps no
//     registry of who holds what (invariant 1: the grant is the record).
//     What the Issuer does see is every member cert presented at
//     #bundle — hubkey-signed {aud: NodeId, cav.name, cav.groups},
//     verified by verifyMember — and that cert IS the proof of the
//     binding. The map ships those certs as-is: the receiver resolves
//     their issuer through the bundled speak-as exactly as it does for
//     its own, so there is no second signed-document format (ADR-0024 I).
//   - NodeId → endpoints is the producer's own reach-me-at, piggybacked
//     on the member's envelope into the Actor's location cache. The hub
//     relays it; it never issues one on a member's behalf (xwz).
//
// The cache is safe-to-lose (ADR-0019): it fills as members beat and
// dies with the process. Losing it degrades to "name unknown", never
// to a wrong answer — a stale cert cannot be forged into the map, only
// omitted from it. A dialing convenience, never an authorization input.

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/marnyg/talos-config/protocol/cert"
)

// NameEntry is one member as the map knows it: the member cert that
// proves name → NodeId, and — when the member's last envelope carried
// one that is still live — its self-issued reach-me-at.
type NameEntry struct {
	Member   cert.Cert
	Location *cert.Cert
}

// witness records a verified member cert as its NodeId's current
// binding. A newer cert (by iat) for the same key replaces an older;
// an older one arriving late (a member beating with a stale kit at a
// hub that already saw its renewal) does not roll it back.
func (i *Issuer) witness(m cert.Cert) {
	id := cert.ActorID(m.Aud)
	i.mu.Lock()
	defer i.mu.Unlock()
	if cur, ok := i.members[id]; ok && cur.Iat > m.Iat {
		return
	}
	i.members[id] = m
}

// nameMap projects the witnessed members: unexpired at now, off the
// blocklist (a listed member is unreachable by policy, so its name
// stops resolving on the same beat its certs stop renewing — j0b),
// joined with each one's cached location. Sorted by name, then NodeId,
// so two beats against the same state produce the same bytes. Expired
// entries are evicted on the way through.
func (i *Issuer) nameMap(now int64, blocklist []cert.ActorID) []NameEntry {
	i.mu.Lock()
	out := make([]NameEntry, 0, len(i.members))
	for id, m := range i.members {
		if m.Exp <= now {
			delete(i.members, id)
			continue
		}
		if slices.Contains(blocklist, id) {
			continue
		}
		out = append(out, NameEntry{Member: m})
	}
	i.mu.Unlock()
	for k := range out {
		out[k].Location = i.Actor.GetLocation(cert.ActorID(out[k].Member.Aud))
	}
	slices.SortFunc(out, func(a, b NameEntry) int {
		return cmp.Or(cmp.Compare(a.Member.Cav.Name, b.Member.Cav.Name), cmp.Compare(a.Member.Aud, b.Member.Aud))
	})
	return out
}

type wireNameEntry struct {
	Member   json.RawMessage `json:"member"`
	Location json.RawMessage `json:"location,omitempty"`
}

func encodeNameMap(entries []NameEntry) ([]wireNameEntry, error) {
	w := make([]wireNameEntry, 0, len(entries))
	for _, e := range entries {
		raw, err := cert.Encode(e.Member)
		if err != nil {
			return nil, err
		}
		we := wireNameEntry{Member: raw}
		if e.Location != nil {
			if we.Location, err = cert.Encode(*e.Location); err != nil {
				return nil, err
			}
		}
		w = append(w, we)
	}
	return w, nil
}

// decodeNameMap parses the wire form, checking each entry's shape: a
// verified member cert, and a location that is a verified reach-me-at
// issued by that same member. It does NOT check expiry or resolve the
// issuer — that is the receiver's job against its own clock and
// consents, the same as for the grants.
func decodeNameMap(w []wireNameEntry) ([]NameEntry, error) {
	out := make([]NameEntry, 0, len(w))
	for k, we := range w {
		m, err := cert.DecodeCert(we.Member)
		if err != nil {
			return nil, fmt.Errorf("issuer: name map %d: %w", k, err)
		}
		if m.Can != cert.VerbMember {
			return nil, fmt.Errorf("issuer: name map %d: can=%q is not a member cert", k, m.Can)
		}
		if err := cert.Verify(m); err != nil {
			return nil, fmt.Errorf("issuer: name map %d: %w", k, err)
		}
		e := NameEntry{Member: m}
		if len(we.Location) > 0 {
			loc, err := cert.DecodeCert(we.Location)
			if err != nil {
				return nil, fmt.Errorf("issuer: name map %d location: %w", k, err)
			}
			if loc.Can != cert.VerbReachMeAt || loc.Iss != cert.ActorID(m.Aud) {
				return nil, fmt.Errorf("issuer: name map %d location: not %s's reach-me-at", k, m.Aud)
			}
			if err := cert.Verify(loc); err != nil {
				return nil, fmt.Errorf("issuer: name map %d location: %w", k, err)
			}
			e.Location = &loc
		}
		out = append(out, e)
	}
	return out, nil
}

// Lookup finds the entries whose member cert carries name. Several
// entries may share a name across a re-key (two NodeIds, both holding
// unexpired certs for the same role until the old one lapses); the
// caller picks by iat or dials both.
func Lookup(entries []NameEntry, name string) []NameEntry {
	var out []NameEntry
	for _, e := range entries {
		if e.Member.Cav.Name == name {
			out = append(out, e)
		}
	}
	return out
}
