package mesh

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

func nebTestParams() HubParams {
	return HubParams{
		Master:     nebTestMaster,
		Subnet:     netip.MustParsePrefix("10.42.0.0/16"),
		ListenHost: "0.0.0.0",
		ListenPort: 4242,
		ServeDNS:   true,
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
	p.Now = func() time.Time { return frozen }
	raw, err := HubConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var got ConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if want := frozen.Add(-ClockSkew); !crt.NotBefore().Equal(want) {
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
	raw, err := HubConfig(nebTestParams())
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
	raw, err := HubConfig(p)
	if err != nil {
		t.Fatal(err)
	}

	var got ConfigYAML
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	wantCA, err := nebderive.CACertPEM(p.Master)
	if err != nil {
		t.Fatal(err)
	}
	if got.PKI.CA != string(wantCA) {
		t.Error("pki.ca is not the derived CA")
	}
	priv, _ := nebderive.HubKey(p.Master)
	if got.PKI.Key != string(nebderive.HostKeyPEM(priv)) {
		t.Error("pki.key is not the derived hub key")
	}

	crt, _, err := cert.UnmarshalCertificateFromPEM([]byte(got.PKI.Cert))
	if err != nil {
		t.Fatal(err)
	}
	hubIP, err := nebderive.HubIP(p.Subnet)
	if err != nil {
		t.Fatal(err)
	}
	if crt.Name() != nebderive.HubName {
		t.Errorf("hub cert name = %q, want %q", crt.Name(), nebderive.HubName)
	}
	if nets := crt.Networks(); len(nets) != 1 || nets[0] != netip.PrefixFrom(hubIP, p.Subnet.Bits()) {
		t.Errorf("hub cert networks = %v, want [%s/%d]", crt.Networks(), hubIP, p.Subnet.Bits())
	}
	ca, err := nebderive.CACert(p.Master)
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
	raw, err := HubConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	var got ConfigYAML
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

	// tcp/80 admits exactly the two device groups (per-route gates in
	// serveMeshHTTP decide who sees what) and never machines: served
	// configs carry other machines' secrets, and machine certs must be
	// dropped by the firewall before a request is accepted.
	httpGroups := map[string]bool{}
	for _, r := range got.Firewall.Inbound {
		if r.Port != "80" || r.Proto != "tcp" {
			continue
		}
		if r.Host == "any" {
			t.Error("tunnel http must not be open to any host")
		}
		if r.Group == "" {
			t.Error("tunnel http rule must be group-scoped")
		}
		httpGroups[r.Group] = true
	}
	if !httpGroups[GroupAdmins] || !httpGroups[GroupMedia] {
		t.Errorf("tunnel http groups = %v, want %q and %q", httpGroups, GroupAdmins, GroupMedia)
	}
	if httpGroups[GroupMachines] {
		t.Error("tunnel http must not admit machine certs")
	}
}

func TestHubNebulaConfigServeDNSGatesRule(t *testing.T) {
	for _, serve := range []bool{true, false} {
		p := nebTestParams()
		p.ServeDNS = serve
		raw, err := HubConfig(p)
		if err != nil {
			t.Fatal(err)
		}
		var got ConfigYAML
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
	tests := map[string]func(*HubParams){
		"zero port":       func(p *HubParams) { p.ListenPort = 0 },
		"port too large":  func(p *HubParams) { p.ListenPort = 70000 },
		"empty host":      func(p *HubParams) { p.ListenHost = "" },
		"bad subnet":      func(p *HubParams) { p.Subnet = netip.Prefix{} },
		"subnet too tiny": func(p *HubParams) { p.Subnet = netip.MustParsePrefix("10.42.0.0/31") },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := nebTestParams()
			mutate(&p)
			if _, err := HubConfig(p); err == nil {
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
	p.ListenHost = nebFlyListenHost
	raw, err := HubConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "host: "+nebFlyListenHost) {
		t.Errorf("listen.host not passed through verbatim:\n%s", raw)
	}
}
