package main

import "testing"

func TestBridgeFlag(t *testing.T) {
	var b bridges
	for _, s := range []string{"cp1/apid=127.0.0.1:50000", "CP1/kube-api=127.0.0.1:6443"} {
		if err := b.Set(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if len(b) != 2 || b[0] != (bridge{"cp1", "apid", "127.0.0.1:50000"}) || b[1].Name != "cp1" || b[1].Facet != "kube-api" {
		t.Fatalf("parsed %+v", b)
	}
	if got := b.String(); got != "cp1/apid=127.0.0.1:50000,cp1/kube-api=127.0.0.1:6443" {
		t.Fatalf("String: %s", got)
	}
	for _, bad := range []string{"cp1=127.0.0.1:1", "cp1/apid", "/apid=127.0.0.1:1", "cp1/apid=nope"} {
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
	for _, bad := range []string{"ssh", "ssh=nope", "apid=127.0.0.1:50000", "ingress-http=127.0.0.1:80", "ssh=127.0.0.1:2222"} {
		if err := f.Set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
