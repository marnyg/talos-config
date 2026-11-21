package main

import "testing"

func TestBridgeFlag(t *testing.T) {
	var b bridges
	for _, s := range []string{"cp1/apid=127.0.0.1:50000", "CP1/kube-api=127.0.0.1:6443"} {
		if err := b.Set(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if len(b) != 2 || b[0] != (bridge{Name: "cp1", Facet: "apid", Listen: "127.0.0.1:50000"}) || b[1].Name != "cp1" || b[1].Facet != "kube-api" {
		t.Fatalf("parsed %+v", b)
	}
	if got := b.String(); got != "cp1/apid=127.0.0.1:50000,cp1/kube-api=127.0.0.1:6443" {
		t.Fatalf("String: %s", got)
	}
	// A facet that takes any port (ADR-0050) is bridged one port at a
	// time: the port rides the facet, as it does in the advertisement.
	var any bridges
	if err := any.Set("nixos/tcp@8080=127.0.0.1:18080"); err != nil {
		t.Fatal(err)
	}
	if any[0] != (bridge{Name: "nixos", Facet: "tcp", Listen: "127.0.0.1:18080", Port: 8080}) || any.String() != "nixos/tcp@8080=127.0.0.1:18080" {
		t.Fatalf("parsed %+v (%s)", any, any.String())
	}
	for _, bad := range []string{"cp1=127.0.0.1:1", "cp1/apid", "/apid=127.0.0.1:1", "cp1/apid=nope",
		"nixos/tcp@=127.0.0.1:1", "nixos/tcp@0=127.0.0.1:1", "nixos/tcp@*=127.0.0.1:1", "nixos/tcp@70000=127.0.0.1:1", "nixos/@80=127.0.0.1:1"} {
		if err := b.Set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestForwardFlag(t *testing.T) {
	var f forwards
	if err := f.Set("ssh=127.0.0.1:22"); err != nil {
		t.Fatal(err)
	}
	if f["ssh"] != "127.0.0.1:22" || f.String() != "ssh=127.0.0.1:22" {
		t.Fatalf("parsed %v", f)
	}
	// Any port on a host (ADR-0050): the port position is `*`.
	if err := f.Set("tcp=127.0.0.1:*"); err != nil {
		t.Fatal(err)
	}
	if f["tcp"] != "127.0.0.1:*" || f.String() != "ssh=127.0.0.1:22,tcp=127.0.0.1:*" {
		t.Fatalf("parsed %v", f)
	}
	for _, bad := range []string{"ssh", "ssh=nope", "apid=127.0.0.1:50000", "ingress-http=127.0.0.1:80", "ssh=127.0.0.1:2222", "tcp=*"} {
		if err := f.Set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
