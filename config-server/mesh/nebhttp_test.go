package mesh

import (
	"net/netip"
	"reflect"
	"testing"
)

// TestBuildHostsList pins the /hosts merge semantics: hub always
// online, machine liveness from the peer map, live devices online by
// construction, static names shadow device names, output name-sorted.
func TestBuildHostsList(t *testing.T) {
	static := map[string]netip.Addr{
		"hub": netip.MustParseAddr("10.42.0.1"),
		"cp1": netip.MustParseAddr("10.42.218.125"),
		"w1":  netip.MustParseAddr("10.42.7.7"),
	}
	live := map[string]netip.Addr{
		"tv": netip.MustParseAddr("10.42.9.9"),
		// A device that somehow claims a static name must not produce a
		// duplicate row; the static (git) entry wins.
		"cp1": netip.MustParseAddr("10.42.99.99"),
	}
	online := map[netip.Addr]bool{
		netip.MustParseAddr("10.42.218.125"): true, // cp1 has a live tunnel
		netip.MustParseAddr("10.42.9.9"):     true, // the tv itself
		// w1 absent: offline. hub absent: must still be online.
	}

	got := buildHostsList(static, live, online)
	want := []hostEntry{
		{Name: "cp1", IP: "10.42.218.125", Kind: "machine", Online: true},
		{Name: "hub", IP: "10.42.0.1", Kind: "hub", Online: true},
		{Name: "tv", IP: "10.42.9.9", Kind: "device", Online: true},
		{Name: "w1", IP: "10.42.7.7", Kind: "machine", Online: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildHostsList:\n got %+v\nwant %+v", got, want)
	}
}
