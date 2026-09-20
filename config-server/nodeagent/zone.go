package nodeagent

// The zone rule: how every presentation (irohup's tun, the Android
// app's) reads a `*.mesh.internal` name and a port as (member, facet).
// Pure over the name map, so it tests C-free; Agent.Zone / Agent.Target
// (caller.go) bind it to the agent's Resolve.

import (
	"errors"
	"fmt"
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
// the hub.
func KindOf(name string, entries []issuer.NameEntry) policy.Kind {
	if name == HubName {
		return policy.KindHub
	}
	for _, e := range entries {
		if e.Location == nil {
			continue
		}
		for _, f := range e.Location.Cav.Facet {
			if k := policy.KindOf(f); k != "" {
				return k
			}
		}
	}
	return policy.KindNode
}

// zone reads a bare zone name (`<name>.mesh.internal` minus the zone):
// one label is a member, read in its own kind's port vocabulary; two
// labels `<svc>.<member>` is a service on a gateway — the member must
// advertise a gateway facet, so a name under a node (`jackett.cp1`,
// still nebula's) is unknown here, not shadowed. resolve is the
// directory (Agent.Resolve). Errors: resolve's (ErrNotBeaten,
// ErrUnknownName), ErrNotThatKind.
func zone(name string, resolve func(string) ([]issuer.NameEntry, error)) (member string, kind policy.Kind, err error) {
	labels := strings.Split(name, ".")
	switch len(labels) {
	case 1:
		member = name
	case 2:
		member = labels[1]
	default:
		return "", "", fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	if member == "" || (len(labels) == 2 && labels[0] == "") {
		return "", "", fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	entries, err := resolve(member)
	if err != nil {
		return "", "", err
	}
	kind = KindOf(member, entries)
	if len(labels) == 2 && kind != policy.KindGateway {
		return "", "", fmt.Errorf("%w: %q is a %s", ErrNotThatKind, member, kind)
	}
	return member, kind, nil
}

// target is zone plus the port: what a flow to <name>:<port> means, as
// (member, facet). A port that is no facet of the kind is a plain
// error: not staleness, nothing a beat would cure.
func target(name string, port uint16, resolve func(string) ([]issuer.NameEntry, error)) (member, facet string, err error) {
	member, kind, err := zone(name, resolve)
	if err != nil {
		return "", "", err
	}
	facet = policy.FacetByPort(kind, port)
	if facet == "" {
		return "", "", fmt.Errorf("nodeagent: port %d is not a %s facet", port, kind)
	}
	return member, facet, nil
}
