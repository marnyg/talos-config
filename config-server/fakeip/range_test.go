package fakeip

import (
	"net"
	"testing"
)

func TestIsFake(t *testing.T) {
	cases := map[string]bool{
		TunIP:               true,
		ResolverIP:          true,
		"198.18.1.1":        true,
		"198.19.255.254":    true,
		"198.20.0.1":        false,
		"198.17.255.255":    false,
		"192.168.1.10":      false,
		"127.0.0.1":         false,
		"::ffff:198.18.0.1": true, // mapped form, as net.ParseIP yields for v4
	}
	for s, want := range cases {
		if got := IsFake(net.ParseIP(s)); got != want {
			t.Errorf("IsFake(%s) = %v, want %v", s, got, want)
		}
	}
	if IsFake(nil) {
		t.Error("IsFake(nil) = true")
	}
}
