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
// The port is read off the member's `<facet>@<port>` advertisement
// (ADR-0047); a record from before that advertises bare facets only
// and is read in its kind's vocabulary.
func TestZoneRule(t *testing.T) {
	dir := map[string][]issuer.NameEntry{
		"cp1":    {serving("ed:cp1", "cp1", "apid", "kube-api")},  // pre-0047: bare facets only
		"gw":     {serving("ed:gw", "gw", "ingress-http", "rdp")}, // pre-0047
		"laptop": {entry("ed:laptop", "laptop", 1000, 1000)},      // advertises nothing
		"old":    {entry("ed:old", "old", 1000, 0)},               // no location at all
		"hub":    {entry("ed:hub", "hub", 1000, 1000)},
		// post-0047: `probe` is in no Go table, its port is still readable.
		"gw2": {serving("ed:gw2", "gw2", "ingress-http", "ingress-http@80", "rdp", "rdp@3389", "probe", "probe@4242")},
		// post-0047 but advertising a port for only one of its facets: a
		// port the table knows for the kind is NOT read from the table
		// once the member advertises ports at all.
		"gw3": {serving("ed:gw3", "gw3", "ingress-http", "rdp", "rdp@3389")},
		// a device gaining a facet with a non-natural port (its sshd on 2222)
		"mac": {serving("ed:mac", "mac", "ssh", "ssh@2222")},
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
		{"jellyfin.gw", 80, "gw", "ingress-http", nil}, // the retired splice's name, now just an ingress host
		{"rdp.gw", 3389, "gw", "rdp", nil},
		{"cp1", 3389, "", "", errPlain},             // rdp is a gateway facet, not a node's
		{"jackett.cp1", 80, "", "", ErrNotThatKind}, // still nebula's
		{"x.hub", 80, "", "", ErrNotThatKind},
		{"laptop", 50000, "laptop", "apid", nil}, // advertises nothing ⇒ read as a node (a device that serves nothing)
		{"old", 50000, "old", "apid", nil},
		{"gw2", 4242, "gw2", "probe", nil}, // (a) read off the advertisement, no table knows `probe`
		{"probe.gw2", 4242, "gw2", "probe", nil},
		{"gw2", 80, "gw2", "ingress-http", nil},
		{"rdp.gw2", 3389, "gw2", "rdp", nil},
		{"gw2", 22000, "", "", errPlain}, // not advertised, even though the gateway table has 22000
		{"gw3", 3389, "gw3", "rdp", nil}, // advertised
		{"gw3", 80, "", "", errPlain},    // advertises ports, not this one: no table fallback
		{"mac", 2222, "mac", "ssh", nil}, // the advertised port, not the natural one
		{"mac", 22, "", "", errPlain},    // the natural port is not what the member advertises
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
// `<facet>@<port>` entries (ADR-0047) carry no kind and are skipped.
func TestKindOf(t *testing.T) {
	entries := []issuer.NameEntry{entry("ed:new", "gw", 1000, 0), serving("ed:old", "gw", "rdp")}
	if k := KindOf("gw", entries); k != "gateway" {
		t.Errorf("KindOf = %s", k)
	}
	if k := KindOf("gw", []issuer.NameEntry{serving("ed:x", "gw", "probe@4242", "rdp@3389", "rdp")}); k != "gateway" {
		t.Errorf("@ entries should not change the kind, got %s", k)
	}
	if k := KindOf("gw", []issuer.NameEntry{serving("ed:x", "gw", "rdp@3389")}); k != "node" {
		t.Errorf("an @ entry alone names no kind, got %s", k)
	}
	if k := KindOf("gw", []issuer.NameEntry{serving("ed:x", "gw", "not-a-facet")}); k != "node" {
		t.Errorf("unknown facet should fall back to node, got %s", k)
	}
	if k := KindOf("hub", nil); k != "hub" {
		t.Errorf("hub: %s", k)
	}
}

// Advertise and splitAdvert round-trip; bare and malformed entries are
// not ports.
func TestAdvertise(t *testing.T) {
	if s := Advertise("syncthing", 22000); s != "syncthing@22000" {
		t.Fatalf("Advertise = %q", s)
	}
	if f, p, ok := splitAdvert("syncthing@22000"); !ok || f != "syncthing" || p != 22000 {
		t.Errorf("splitAdvert = %q %d %v", f, p, ok)
	}
	for _, bad := range []string{"rdp", "rdp@", "@80", "rdp@0", "rdp@70000", "rdp@x", ""} {
		if _, _, ok := splitAdvert(bad); ok {
			t.Errorf("splitAdvert(%q) should not parse", bad)
		}
	}
}
