package nodeagent

import (
	"bytes"
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

// A facet that takes any port (ADR-0050) is advertised as `<facet>@*`:
// route falls back to it for every port the member advertises no facet
// at, and carries the port; a facet advertised at exactly that port
// wins. target — the exact reading the :443 fiction asks about :80 —
// never answers with it. A daemon from before ADR-0050 skips the
// entry: it is neither a port (splitAdvert) nor a kind (KindOf).
func TestRouteAnyPort(t *testing.T) {
	dir := map[string][]issuer.NameEntry{
		// a device forwarding its sshd and any port
		"nixos": {serving("ed:nixos", "nixos", "ssh", "ssh@22", "tcp", "tcp@*")},
		// any port only: must not be read as a pre-0047 record
		"mac": {serving("ed:mac", "mac", "tcp", "tcp@*")},
		// a facet name no table knows, taking any port on a gateway
		"gw":  {serving("ed:gw", "gw", "ingress-http", "ingress-http@80", "lab", "lab@*")},
		"cp1": {serving("ed:cp1", "cp1", "apid", "apid@50000")},
	}
	resolve := func(name string) ([]issuer.NameEntry, error) {
		if e, ok := dir[name]; ok {
			return e, nil
		}
		return nil, ErrUnknownName
	}
	cases := []struct {
		name string
		port uint16
		want Route
		err  error
	}{
		{"nixos", 22, Route{Member: "nixos", Facet: "ssh"}, nil}, // the named facet wins its port
		{"nixos", 8080, Route{Member: "nixos", Facet: "tcp", Port: 8080}, nil},
		{"nixos", 443, Route{Member: "nixos", Facet: "tcp", Port: 443}, nil},
		{"nixos", 1, Route{Member: "nixos", Facet: "tcp", Port: 1}, nil}, // the vocabulary's nominal port is a port like any other
		{"mac", 22, Route{Member: "mac", Facet: "tcp", Port: 22}, nil},   // no ssh facet: :22 is just a port
		{"mac", 50000, Route{Member: "mac", Facet: "tcp", Port: 50000}, nil},
		{"nixos", 0, Route{}, ErrNotFacet},
		{"gw", 80, Route{Member: "gw", Facet: "ingress-http"}, nil},
		{"gw", 5432, Route{Member: "gw", Facet: "lab", Port: 5432}, nil},
		{"db.gw", 5432, Route{Member: "gw", Facet: "lab", Port: 5432}, nil},
		{"cp1", 50000, Route{Member: "cp1", Facet: "apid"}, nil},
		{"cp1", 8080, Route{}, ErrNotFacet}, // no member gains a port it did not offer
		{"nope", 8080, Route{}, ErrUnknownName},
	}
	for _, c := range cases {
		got, err := route(c.name, c.port, resolve)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("route(%s, %d) = %+v, %v; want %+v, %v", c.name, c.port, got, err, c.want, c.err)
		}
		// target agrees wherever the port is a facet's own and refuses
		// wherever only the any-port facet answers.
		member, facet, terr := target(c.name, c.port, resolve)
		switch {
		case c.err != nil && !errors.Is(terr, c.err):
			t.Errorf("target(%s, %d): %v, want %v", c.name, c.port, terr, c.err)
		case c.err == nil && c.want.Port != 0 && !errors.Is(terr, ErrNotFacet):
			t.Errorf("target(%s, %d) = %s/%s %v; the exact reading must not answer with an any-port facet", c.name, c.port, member, facet, terr)
		case c.err == nil && c.want.Port == 0 && (terr != nil || member != c.want.Member || facet != c.want.Facet):
			t.Errorf("target(%s, %d) = %s/%s %v", c.name, c.port, member, facet, terr)
		}
	}
	if k := KindOf("mac", dir["mac"]); k != "device" {
		t.Errorf("a member advertising tcp reads as a device, got %s", k)
	}
	if k := KindOf("x", []issuer.NameEntry{serving("ed:x", "x", "tcp@*")}); k != "node" {
		t.Errorf("an @* entry alone names no kind, got %s", k)
	}
	if s := AdvertiseAny("tcp"); s != "tcp@*" {
		t.Fatalf("AdvertiseAny = %q", s)
	}
	if _, _, ok := splitAdvert("tcp@*"); ok {
		t.Error("an any-port entry is not a port: a pre-0050 daemon must skip it")
	}
	for _, bad := range []string{"tcp", "tcp@", "@*", "tcp@**", "tcp@80", ""} {
		if _, ok := splitAdvertAny(bad); ok {
			t.Errorf("splitAdvertAny(%q) should not parse", bad)
		}
	}
}

// The port header round-trips, and neither side lets port 0 through.
func TestPortHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := WritePort(&buf, 8080); err != nil {
		t.Fatal(err)
	}
	buf.WriteString("payload")
	if !bytes.Equal(buf.Bytes()[:2], []byte{0x1f, 0x90}) {
		t.Fatalf("header % x, want big-endian 8080", buf.Bytes()[:2])
	}
	if p, err := ReadPort(&buf); err != nil || p != 8080 || buf.String() != "payload" {
		t.Fatalf("ReadPort = %d, %v; rest %q", p, err, buf.String())
	}
	if err := WritePort(&buf, 0); err == nil {
		t.Error("WritePort(0) accepted")
	}
	if _, err := ReadPort(bytes.NewReader([]byte{0, 0})); err == nil {
		t.Error("ReadPort accepted port 0")
	}
	if _, err := ReadPort(bytes.NewReader([]byte{0x1f})); err == nil {
		t.Error("ReadPort accepted a short header")
	}
}

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
