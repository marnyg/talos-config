package nodeagent

import (
	"errors"
	"testing"

	"github.com/marnyg/talos-config/config-server/issuer"
)

// serving is entry plus the facets the member's reach-me-at advertises.
func serving(id, name string, facets ...string) issuer.NameEntry {
	e := entry(id, name, 1000, 1000)
	e.Location.Cav.Facet = facets
	return e
}

// The zone rule (P2.3, decision on 359.9.3): one label is a member in
// its advertised kind; `<svc>.<member>` resolves only on a gateway, so
// a service name under a node stays nebula's during the dual plane.
func TestZoneRule(t *testing.T) {
	dir := map[string][]issuer.NameEntry{
		"cp1":    {serving("ed:cp1", "cp1", "apid", "kube-api")},
		"gw":     {serving("ed:gw", "gw", "ingress-http", "jellyfin")},
		"laptop": {entry("ed:laptop", "laptop", 1000, 1000)}, // advertises nothing
		"old":    {entry("ed:old", "old", 1000, 0)},          // no location at all
		"hub":    {entry("ed:hub", "hub", 1000, 1000)},
	}
	resolve := func(name string) ([]issuer.NameEntry, error) {
		if e, ok := dir[name]; ok {
			return e, nil
		}
		return nil, ErrUnknownName
	}
	cases := []struct {
		name   string
		port   uint16
		member string
		facet  string
		err    error // nil ⇒ ok; ErrUnknownName / ErrNotThatKind; errPlain ⇒ any other error
	}{
		{"cp1", 50000, "cp1", "apid", nil},
		{"cp1", 6443, "cp1", "kube-api", nil},
		{"cp1", 80, "", "", errPlain}, // no node facet on 80: not staleness
		{"hub", 80, "hub", "hub-http", nil},
		{"gw", 80, "gw", "ingress-http", nil}, // a bare gateway name reads in its own vocabulary too (nginx answers 404 for that Host)
		{"jackett.gw", 80, "gw", "ingress-http", nil},
		{"jellyfin.gw", 8096, "gw", "jellyfin", nil},
		{"jackett.cp1", 80, "", "", ErrNotThatKind}, // still nebula's
		{"x.hub", 80, "", "", ErrNotThatKind},
		{"laptop", 50000, "laptop", "apid", nil}, // advertises nothing ⇒ read as a node (≤ 0.1.3 agents)
		{"old", 50000, "old", "apid", nil},
		{"nope", 80, "", "", ErrUnknownName},
		{"a.b.c", 80, "", "", ErrUnknownName},
		{".gw", 80, "", "", ErrUnknownName},
		{"", 80, "", "", ErrUnknownName},
	}
	for _, c := range cases {
		member, facet, err := target(c.name, c.port, resolve)
		switch {
		case c.err == nil && err != nil:
			t.Errorf("%s:%d: unexpected error %v", c.name, c.port, err)
		case c.err == errPlain && (err == nil || errors.Is(err, ErrUnknownName) || errors.Is(err, ErrNotThatKind)):
			t.Errorf("%s:%d: want a plain error, got %v", c.name, c.port, err)
		case c.err != nil && c.err != errPlain && !errors.Is(err, c.err):
			t.Errorf("%s:%d: want %v, got %v", c.name, c.port, c.err, err)
		case err == nil && (member != c.member || facet != c.facet):
			t.Errorf("%s:%d: got (%s, %s), want (%s, %s)", c.name, c.port, member, facet, c.member, c.facet)
		}
	}
	// Zone alone (the resolver's Known): a service under a gateway is a
	// name; under a node it is not, with the kind-mismatch error so the
	// tun does not kick a beat for it.
	if m, k, err := zone("jackett.gw", resolve); err != nil || m != "gw" || k != "gateway" {
		t.Errorf("jackett.gw: %s %s %v", m, k, err)
	}
	if _, _, err := zone("sonarr.cp1", resolve); !errors.Is(err, ErrNotThatKind) || errors.Is(err, ErrUnknownName) {
		t.Errorf("sonarr.cp1: %v", err)
	}
}

var errPlain = errors.New("plain")

// KindOf reads the first recognised advertised facet; a newer entry
// without a location does not hide an older one's advertisement.
func TestKindOf(t *testing.T) {
	entries := []issuer.NameEntry{entry("ed:new", "gw", 1000, 0), serving("ed:old", "gw", "jellyfin")}
	if k := KindOf("gw", entries); k != "gateway" {
		t.Errorf("KindOf = %s", k)
	}
	if k := KindOf("gw", []issuer.NameEntry{serving("ed:x", "gw", "not-a-facet")}); k != "node" {
		t.Errorf("unknown facet should fall back to node, got %s", k)
	}
	if k := KindOf("hub", nil); k != "hub" {
		t.Errorf("hub: %s", k)
	}
}
