package main

import (
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/cert"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/logging"
	"github.com/slackhq/nebula/overlay"

	"github.com/marnyg/talos-config/config-server/nebderive"
)

var nebTestMaster = []byte("nebconf-test-master-key-32-bytes")

func nebTestParams() nebHubParams {
	return nebHubParams{
		master:     nebTestMaster,
		subnet:     netip.MustParsePrefix("10.42.0.0/16"),
		listenHost: "0.0.0.0",
		listenPort: 4242,
		serveDNS:   true,
		// Real clock on purpose: nebula validates the cert's window
		// against wall time, so a frozen clock would either be rejected
		// as future-dated or quietly stop testing anything. The window
		// itself is pinned in TestHubCertValidityWindow.
	}
}

// TestHubCertValidityWindow pins the window against a frozen clock: the
// hub's cert is backdated by the skew allowance so peers whose clocks
// trail the hub's still accept it.
func TestHubCertValidityWindow(t *testing.T) {
	frozen := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	p := nebTestParams()
	p.now = func() time.Time { return frozen }
	raw, err := hubNebulaConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var got nebConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if want := frozen.Add(-nebClockSkew); !crt.NotBefore().Equal(want) {
		t.Errorf("notBefore = %s, want %s (frozen now minus skew)", crt.NotBefore(), want)
	}
	if want := frozen.Add(nebHubCertValidity); !crt.NotAfter().Equal(want) {
		t.Errorf("notAfter = %s, want %s", crt.NotAfter(), want)
	}
}

// TestHubNebulaConfigValidates is the load-bearing test: nebula's own
// validation accepts the config we render. configTest=true runs every
// config-driven constructor (pki, firewall, lighthouse, relay, listen)
// without starting the tunnel, so a typo'd key or a malformed rule fails
// here rather than at unseal time on fly.
func TestHubNebulaConfigValidates(t *testing.T) {
	raw, err := hubNebulaConfig(nebTestParams())
	if err != nil {
		t.Fatal(err)
	}

	var c config.C
	if err := c.LoadString(string(raw)); err != nil {
		t.Fatalf("nebula cannot parse the rendered config: %v\n%s", err, raw)
	}
	if _, err := nebula.Main(&c, true, "nebconf-test", logging.NewLogger(io.Discard), overlay.NewUserDeviceFromConfig); err != nil {
		t.Fatalf("nebula rejected the rendered config: %v\n%s", err, raw)
	}
}

// TestHubNebulaConfigDerivedValues pins the values that must come from
// the derivation rather than from anywhere else: the hub's cert is the
// derived hub identity at the reserved first host address, signed by the
// derived CA.
func TestHubNebulaConfigDerivedValues(t *testing.T) {
	p := nebTestParams()
	raw, err := hubNebulaConfig(p)
	if err != nil {
		t.Fatal(err)
	}

	var got nebConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	wantCA, err := nebderive.CACertPEM(p.master)
	if err != nil {
		t.Fatal(err)
	}
	if got.PKI.CA != string(wantCA) {
		t.Error("pki.ca is not the derived CA")
	}
	priv, _ := nebderive.HubKey(p.master)
	if got.PKI.Key != string(nebderive.HostKeyPEM(priv)) {
		t.Error("pki.key is not the derived hub key")
	}

	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	hubIP, err := nebderive.HubIP(p.subnet)
	if err != nil {
		t.Fatal(err)
	}
	if crt.Name() != nebderive.HubName {
		t.Errorf("hub cert name = %q, want %q", crt.Name(), nebderive.HubName)
	}
	if nets := crt.Networks(); len(nets) != 1 || nets[0] != netip.PrefixFrom(hubIP, p.subnet.Bits()) {
		t.Errorf("hub cert networks = %v, want [%s/%d]", crt.Networks(), hubIP, p.subnet.Bits())
	}
	ca, err := nebderive.CACert(p.master)
	if err != nil {
		t.Fatal(err)
	}
	caFP, err := ca.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if crt.Issuer() != caFP {
		t.Error("hub cert is not issued by the derived CA")
	}
}

// TestHubNebulaConfigInvariants guards the properties that make the hub
// what the design says it is; each of these has a reason recorded in
// nebconf.go and would be easy to flip by accident.
func TestHubNebulaConfigInvariants(t *testing.T) {
	p := nebTestParams()
	raw, err := hubNebulaConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var got nebConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if !got.Lighthouse.AmLighthouse || !got.Relay.AmRelay {
		t.Error("hub must be both lighthouse and relay")
	}
	// The whole reason nebstack exists: nebula's own DNS cannot work
	// without a TUN, so it must stay off even when the hub serves DNS.
	if got.Lighthouse.ServeDNS {
		t.Error("lighthouse.serve_dns must be false on the TUN-less hub")
	}
	// No peer registry: the hub is the lighthouse, membership is the CA
	// signature (invariant 1).
	if len(got.StaticHostMap) != 0 {
		t.Errorf("static_host_map must be empty, got %v", got.StaticHostMap)
	}
	if got.Firewall.InboundAction != "drop" {
		t.Error("inbound firewall must default to drop")
	}

	// /config is admin-only, and specifically not reachable by machines:
	// served configs carry other machines' secrets.
	var httpRule *nebRuleYAML
	for i, r := range got.Firewall.Inbound {
		if r.Port == "80" && r.Proto == "tcp" {
			httpRule = &got.Firewall.Inbound[i]
		}
	}
	if httpRule == nil {
		t.Fatal("no inbound rule for tunnel http")
	}
	if httpRule.Group != nebGroupAdmins {
		t.Errorf("tunnel http rule group = %q, want %q", httpRule.Group, nebGroupAdmins)
	}
	if httpRule.Host == "any" {
		t.Error("tunnel http must not be open to any host")
	}
}

func TestHubNebulaConfigServeDNSGatesRule(t *testing.T) {
	for _, serve := range []bool{true, false} {
		p := nebTestParams()
		p.serveDNS = serve
		raw, err := hubNebulaConfig(p)
		if err != nil {
			t.Fatal(err)
		}
		var got nebConfigYAML
		if err := yaml.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range got.Firewall.Inbound {
			if r.Port == "53" && r.Proto == "udp" {
				found = true
			}
		}
		if found != serve {
			t.Errorf("serveDNS=%v: udp/53 inbound rule present=%v", serve, found)
		}
	}
}

func TestHubNebulaConfigRejectsBadParams(t *testing.T) {
	tests := map[string]func(*nebHubParams){
		"zero port":       func(p *nebHubParams) { p.listenPort = 0 },
		"port too large":  func(p *nebHubParams) { p.listenPort = 70000 },
		"empty host":      func(p *nebHubParams) { p.listenHost = "" },
		"bad subnet":      func(p *nebHubParams) { p.subnet = netip.Prefix{} },
		"subnet too tiny": func(p *nebHubParams) { p.subnet = netip.MustParsePrefix("10.42.0.0/31") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := nebTestParams()
			mutate(&p)
			if _, err := hubNebulaConfig(p); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// TestFlyListenHostIsResolvedByNebula documents why there is no bind
// shim here: nebula resolves listen.host itself, so the fly-global-
// services hostname is passed straight through. Off fly the name does
// not resolve, which is the failure we want to see rather than a silent
// wildcard bind.
func TestFlyListenHostIsResolvedByNebula(t *testing.T) {
	p := nebTestParams()
	p.listenHost = nebFlyListenHost
	raw, err := hubNebulaConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "host: "+nebFlyListenHost) {
		t.Errorf("listen.host not passed through verbatim:\n%s", raw)
	}
}
