package nodeagent

// The zone rule: how every presentation (irohup's tun, the Android
// app's) reads a `*.mesh.internal` name and a port as (member, facet).
// Pure over the name map, so it tests C-free; Agent.Zone / Agent.Target
// (caller.go) bind it to the agent's Resolve.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/policy"
)

// ErrNotBeaten: no bundle yet, so nothing to present and no name map.
var ErrNotBeaten = errors.New("nodeagent: no bundle yet (first beat pending)")

// ErrUnknownName: the name map has no live entry for the name.
var ErrUnknownName = errors.New("nodeagent: name not in the name map")

// ErrNotThatKind: the zone name's depth does not fit the member's kind
// (a service name under a member that serves no gateway facet).
var ErrNotThatKind = errors.New("nodeagent: name depth does not match the member's kind")

// ErrNotFacet: the port is no facet the member's record advertises
// (ADR-0047) — nor, for a record that advertises no ports at all, in
// the vocabulary of the kind it is read as.
var ErrNotFacet = errors.New("nodeagent: port is not a facet the member advertises")

// PortSep separates a facet from its port in a reach-me-at's cav.facet
// (ADR-0047): a receiver advertises `<facet>` and `<facet>@<port>`
// side by side, so a presentation reads the port off the advertisement
// and kind stays what it was — the facet vocabulary a name is grouped
// under. The bare entry is what pre-0047 daemons read; the `@` entry
// is what post-0047 ones do; both are advertisements, never authority.
const PortSep = "@"

// Advertise encodes facet at port for cav.facet.
func Advertise(facet string, port uint16) string {
	return facet + PortSep + strconv.FormatUint(uint64(port), 10)
}

// AnyPort is the port position of a facet that takes every port
// (ADR-0050): a receiver forwards it as `<facet>=<host>:*` and
// advertises it as `<facet>@*`. Such a facet has no port of its own —
// the caller names one at the head of each stream (WritePort), and a
// presentation falls back to it for any port the member advertises no
// facet at. Like every `@` entry it is an advertisement, never
// authority: the grant names the facet, the receiver's consent admits.
const AnyPort = "*"

// AdvertiseAny encodes "facet takes any port" for cav.facet. A daemon
// from before ADR-0050 reads the entry as neither a port nor a kind
// (splitAdvert, KindOf) and skips it.
func AdvertiseAny(facet string) string { return facet + PortSep + AnyPort }

// splitAdvertAny reads an advertised `<facet>@*` entry.
func splitAdvertAny(s string) (facet string, ok bool) {
	f, p, found := strings.Cut(s, PortSep)
	return f, found && f != "" && p == AnyPort
}

// Route is what a flow to <name>:<port> means: the member, the facet
// the stream rides and — when that facet takes any port (ADR-0050) —
// the Port the caller must name at the head of each stream. Port is 0
// for an ordinary facet, whose port is the receiver's own business.
type Route struct {
	Member, Facet string
	Port          uint16
}

// splitAdvert reads an advertised `<facet>@<port>` entry; ok is false
// for a bare facet or a malformed port.
func splitAdvert(s string) (facet string, port uint16, ok bool) {
	f, p, found := strings.Cut(s, PortSep)
	if !found || f == "" {
		return "", 0, false
	}
	n, err := strconv.ParseUint(p, 10, 16)
	if err != nil || n == 0 {
		return "", 0, false
	}
	return f, uint16(n), true
}

// facetAt is the facet the member's live records advertise at port,
// the facet they advertise for any port (`<facet>@*`, ADR-0050; "" if
// none), and whether they advertise in the `@` form at all — the
// readings route needs: a hit, the any-port fallback, a miss on a
// record that does advertise ports (a genuine ErrNotFacet, or one beat
// of staleness), or a record from before ADR-0047 that must be read in
// its kind's vocabulary instead.
func facetAt(entries []issuer.NameEntry, port uint16) (facet, anyPort string, advertised bool) {
	for _, e := range entries {
		if e.Location == nil {
			continue
		}
		for _, s := range e.Location.Cav.Facet {
			if f, ok := splitAdvertAny(s); ok {
				advertised = true
				if anyPort == "" {
					anyPort = f
				}
				continue
			}
			f, p, ok := splitAdvert(s)
			if !ok {
				continue
			}
			advertised = true
			if p == port && facet == "" {
				facet = f
			}
		}
	}
	return facet, anyPort, advertised
}

// HubName is the hub's bare name on the presentation (hub.<zone>). The
// hub is a well-known actor, not a member: it holds no member cert and
// is not in the name map, so Resolve answers this name from the hub
// record the beat keeps (hubkey + reach-me-at). It shadows any member
// the Owner might name "hub".
const HubName = "hub"

// KindOf reads a member's receiver kind off what its reach-me-at
// advertises (actor.Serves → cav.facet; policy.KindOf). A record that
// advertises nothing is read as a node: the harmless reading for a
// device that serves nothing (the dial fails at the ALPN). HubName is
// the hub. `<facet>@<port>` entries (ADR-0047) carry a port, not a
// kind, and are skipped.
func KindOf(name string, entries []issuer.NameEntry) policy.Kind {
	if name == HubName {
		return policy.KindHub
	}
	for _, e := range entries {
		if e.Location == nil {
			continue
		}
		for _, f := range e.Location.Cav.Facet {
			if strings.Contains(f, PortSep) {
				continue
			}
			if k := policy.KindOf(f); k != "" {
				return k
			}
		}
	}
	return policy.KindNode
}

// zone reads a bare zone name (`<name>.mesh.internal` minus the zone):
// one label is a member; two labels `<svc>.<member>` is a service on a
// gateway — the member must advertise a gateway facet, so a name under
// a node (`jackett.cp1`, still nebula's) is unknown here, not
// shadowed. resolve is the directory (Agent.Resolve). Errors:
// resolve's (ErrNotBeaten, ErrUnknownName), ErrNotThatKind.
func zone(name string, resolve func(string) ([]issuer.NameEntry, error)) (member string, kind policy.Kind, err error) {
	member, kind, _, err = zoneEntries(name, resolve)
	return member, kind, err
}

// zoneEntries is zone plus the member's resolved records, so target
// reads the port off the same directory snapshot that read the kind.
func zoneEntries(name string, resolve func(string) ([]issuer.NameEntry, error)) (member string, kind policy.Kind, entries []issuer.NameEntry, err error) {
	labels := strings.Split(name, ".")
	switch len(labels) {
	case 1:
		member = name
	case 2:
		member = labels[1]
	default:
		return "", "", nil, fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	if member == "" || (len(labels) == 2 && labels[0] == "") {
		return "", "", nil, fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	entries, err = resolve(member)
	if err != nil {
		return "", "", nil, err
	}
	kind = KindOf(member, entries)
	if len(labels) == 2 && kind != policy.KindGateway {
		return "", "", nil, fmt.Errorf("%w: %q is a %s", ErrNotThatKind, member, kind)
	}
	return member, kind, entries, nil
}

// target is zone plus the port: what a flow to <name>:<port> means, as
// (member, facet). The facet is the one the member advertises at that
// port (`<facet>@<port>` in its reach-me-at, ADR-0047) — so a facet
// the receiver gained is readable here without this daemon knowing its
// name. A record that advertises no ports at all is a daemon from
// before ADR-0047 and is read in its kind's vocabulary
// (policy.FacetByPort) as before. A port advertised by neither is
// ErrNotFacet — over a directory a beat old it may be staleness after
// all: a member that starts serving (a device gaining ssh, ADR-0038; a
// gateway gaining syncthing, ADR-0046) changes its advertisement under
// a map that still shows the old record. Agent.Target re-beats on it.
//
// target is the exact reading: a facet that takes any port (ADR-0050)
// is not an answer here. It is what the :443 fiction asks about :80 —
// "does this name have an HTTP facet" — and route is what a flow asks.
func target(name string, port uint16, resolve func(string) ([]issuer.NameEntry, error)) (member, facet string, err error) {
	r, err := route(name, port, resolve)
	if err != nil {
		return "", "", err
	}
	if r.Port != 0 {
		return "", "", fmt.Errorf("%w: port %d on %s", ErrNotFacet, port, r.Member)
	}
	return r.Member, r.Facet, nil
}

// route is target plus the any-port fallback (ADR-0050): a facet the
// member advertises at exactly that port wins; failing that, a facet it
// advertises as `<facet>@*` carries the flow with the port named in the
// stream (Route.Port). So a device that forwards both its sshd and any
// port is reached on :22 under the ssh grant and everywhere else under
// the any-port one. Errors are target's.
func route(name string, port uint16, resolve func(string) ([]issuer.NameEntry, error)) (Route, error) {
	member, kind, entries, err := zoneEntries(name, resolve)
	if err != nil {
		return Route{}, err
	}
	facet, anyPort, advertised := facetAt(entries, port)
	if facet == "" && !advertised {
		facet = policy.FacetByPort(kind, port)
	}
	switch {
	case facet != "":
		return Route{Member: member, Facet: facet}, nil
	case anyPort != "" && port != 0:
		return Route{Member: member, Facet: anyPort, Port: port}, nil
	}
	return Route{}, fmt.Errorf("%w: port %d on %s", ErrNotFacet, port, member)
}
