package mesh

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"

	"github.com/marnyg/talos-config/config-server/policyclient"
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

// TestDevicePolicyWire pins the GET /policy wire format against the
// device-side decoder (policyclient): same field names, epoch derived
// from the rules so unrelated scopes don't churn it.
func TestDevicePolicyWire(t *testing.T) {
	rules := []nebRuleYAML{
		{Port: "any", Proto: "icmp", Host: "any"},
		{Port: "18080", Proto: "tcp", Group: GroupMedia},
	}
	body, err := devicePolicyWire(rules)
	if err != nil {
		t.Fatal(err)
	}
	w, err := policyclient.Fetch(fetchStub(t, body))
	if err != nil {
		t.Fatalf("policyclient cannot decode the hub's wire format: %v", err)
	}
	if len(w.Inbound) != 2 || w.Inbound[1].Group != GroupMedia || w.Inbound[1].Port != "18080" {
		t.Errorf("decoded rules = %+v", w.Inbound)
	}

	same, _ := devicePolicyWire(rules)
	if string(same) != string(body) {
		t.Error("wire is not deterministic for identical rules")
	}
	w2, _ := devicePolicyWire([]nebRuleYAML{{Port: "any", Proto: "icmp", Host: "any"}})
	var a, b struct{ Epoch string }
	_ = json.Unmarshal(body, &a)
	_ = json.Unmarshal(w2, &b)
	if a.Epoch == b.Epoch {
		t.Error("different rules produced the same epoch")
	}
}

// fetchStub serves body at /policy and returns (client, base) for
// policyclient.Fetch.
func fetchStub(t *testing.T, body []byte) (*http.Client, string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts.Client(), ts.URL
}
