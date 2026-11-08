package main

import "testing"

func TestForwardFlag(t *testing.T) {
	var f forwards
	for _, s := range []string{"rdp=win2k25-rdp.vms.svc.cluster.local:3389", "probe=svc.ns:4242"} {
		if err := f.Set(s); err != nil {
			t.Fatal(err)
		}
	}
	if f["rdp"] != "win2k25-rdp.vms.svc.cluster.local:3389" || f["probe"] != "svc.ns:4242" {
		t.Fatalf("got %v", f)
	}
	if f.String() != "probe=svc.ns:4242,rdp=win2k25-rdp.vms.svc.cluster.local:3389" {
		t.Fatalf("String() = %q", f.String())
	}
	for _, bad := range []string{"rdp", "=x:1", "rdp=win", "rdp=win:1"} {
		if err := f.Set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
