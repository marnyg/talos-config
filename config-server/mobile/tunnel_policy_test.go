package mobile

// syncPolicy without a running nebula: the reload path only needs a
// loaded config object (ReloadConfigString parses + fires callbacks;
// with no instance there are no callbacks), so the test pins the
// fetch→epoch-compare→splice→reload sequence and the bookkeeping.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nebconfig "github.com/slackhq/nebula/config"
)

const policyTestCfg = `pki:
    ca: |
        -----BEGIN NEBULA CERTIFICATE-----
        dGVzdCBjYQ==
        -----END NEBULA CERTIFICATE-----
lighthouse:
    hosts:
        - 10.42.0.1
firewall:
    outbound_action: drop
    inbound_action: drop
    outbound:
        - port: any
          proto: any
          host: any
    inbound:
        - port: any
          proto: icmp
          host: any
`

func TestSyncPolicy(t *testing.T) {
	var c nebconfig.C
	if err := c.LoadString(policyTestCfg); err != nil {
		t.Fatal(err)
	}
	tun := &Tunnel{cfg: &c, cfgYAML: policyTestCfg}

	body := `{"epoch":"e1","inbound":[{"port":"any","proto":"icmp","host":"any"},{"port":"18080","proto":"tcp","group":"media"}]}`
	var hits int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/policy" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	changed, err := tun.syncPolicy(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("first sync should apply")
	}
	if !strings.Contains(tun.cfgYAML, "18080") {
		t.Error("applied config is missing the new rule")
	}
	if !strings.Contains(tun.cfgYAML, "dGVzdCBjYQ==") {
		t.Error("applied config lost the CA PEM")
	}
	// The running instance's view changed too, not just the string.
	if !c.HasChanged("firewall") {
		t.Error("nebula config object does not see a firewall change")
	}

	// Same epoch: no-op, no reload.
	changed, err = tun.syncPolicy(ts.URL)
	if err != nil || changed {
		t.Errorf("second sync = (%v, %v), want (false, nil)", changed, err)
	}
	if hits != 2 {
		t.Errorf("hub polled %d times, want 2", hits)
	}

	// Unreachable hub: error out, keep state.
	ts.Close()
	if _, err := tun.syncPolicy(ts.URL); err == nil {
		t.Error("expected an error once the hub is unreachable")
	}
	if tun.policyEpoch != "e1" || !strings.Contains(tun.cfgYAML, "18080") {
		t.Error("a failed sync disturbed the applied state")
	}
}
