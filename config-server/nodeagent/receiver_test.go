package nodeagent

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/config-server/policy"
	"github.com/marnyg/talos-config/protocol/cert"
)

func noopHandler(context.Context, io.ReadWriteCloser, cert.Identity, cert.ActorID) {}

// The accept table is exactly Forward ∪ Serve (ADR-0048): a facet the
// compiled vocabulary has never heard of is accepted and advertised at
// its target's port, and a vocabulary facet the member does not forward
// is not in the table. Kind is still validated; host:port and the
// forward/serve overlap still are.
func TestReceiverTableIsForwardUnionServe(t *testing.T) {
	rt, err := receiverTable(policy.KindGateway,
		map[string]string{"rdp": "win:3389", "probe": "svc.ns:4242"},
		map[string]StreamHandler{"ingress-http": noopHandler})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rt.facets, []string{"ingress-http", "probe", "rdp"}) {
		t.Fatalf("facets %v", rt.facets)
	}
	want := map[string]string{policy.ALPN("ingress-http"): "ingress-http", policy.ALPN("probe"): "probe", policy.ALPN("rdp"): "rdp"}
	for alpn, f := range want {
		if rt.accept[alpn] != f {
			t.Errorf("accept[%s] = %q, want %q", alpn, rt.accept[alpn], f)
		}
	}
	if len(rt.accept) != len(want) || len(rt.alpns) != len(want) {
		t.Fatalf("accept %v alpns %v", rt.accept, rt.alpns)
	}
	if _, has := rt.accept[policy.ALPN("syncthing")]; has {
		t.Fatal("a vocabulary facet the member does not forward must not be accepted")
	}
	// Ports: natural for the known, the target's for the unknown.
	if rt.ports["probe"] != 4242 || rt.ports["rdp"] != 3389 || rt.ports["ingress-http"] != 80 {
		t.Fatalf("ports %v", rt.ports)
	}
	if !slices.Contains(rt.adverts, "probe@4242") || !slices.Contains(rt.adverts, "probe") {
		t.Fatalf("adverts %v", rt.adverts)
	}
	// A caller-only member: nothing accepted, nothing advertised.
	rt, err = receiverTable("", nil, nil)
	if err != nil || len(rt.accept) != 0 || len(rt.adverts) != 0 {
		t.Fatalf("caller-only: %v %v", rt, err)
	}
}

func TestReceiverTableRejects(t *testing.T) {
	type in struct {
		kind    policy.Kind
		forward map[string]string
		serve   map[string]StreamHandler
	}
	cases := map[string]in{
		"unknown kind":           {kind: "phone"},
		"bad host:port":          {forward: map[string]string{"rdp": "win"}},
		"unknown facet, no port": {forward: map[string]string{"probe": "svc:http"}},
		"forwarded and served":   {forward: map[string]string{"ingress-http": "x:80"}, serve: map[string]StreamHandler{"ingress-http": noopHandler}},
		"nil handler":            {serve: map[string]StreamHandler{"ingress-http": nil}},
		"served, no port":        {serve: map[string]StreamHandler{"probe": noopHandler}},
		"empty facet":            {forward: map[string]string{"": "x:1"}},
	}
	for name, o := range cases {
		if _, err := receiverTable(o.kind, o.forward, o.serve); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "is not a") && strings.Contains(err.Error(), "facet (") {
			t.Errorf("%s: a vocabulary membership error survived: %v", name, err)
		}
	}
}
