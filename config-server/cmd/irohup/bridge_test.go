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
