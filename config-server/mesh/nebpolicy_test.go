package mesh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/config-server/nebderive"
)

// nebRepoTalos is the enclosing repo's talos/ directory. Policy tests
// and fixtures run against the real shipped mesh-policy.yaml, so this
// package's firewall assertions guard the file itself, not a copy that
// could drift from it.
const nebRepoTalos = "../../talos"

// nebRepoPolicy loads the repo's real policy file.
func nebRepoPolicy(tb testing.TB) *meshPolicy {
	tb.Helper()
	p, err := loadPolicy(nebRepoTalos)
	if err != nil {
		tb.Fatalf("loading repo %s: %v", PolicyFile, err)
	}
	return p
}

// nebInstallPolicy copies the repo's policy file into root, for tests
// that build a Manager over a scratch talos tree.
func nebInstallPolicy(tb testing.TB, root string) {
	tb.Helper()
	raw, err := os.ReadFile(filepath.Join(nebRepoTalos, PolicyFile))
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, PolicyFile), raw, 0o644); err != nil {
		tb.Fatal(err)
	}
}

// nebPolicyRoot is t.TempDir() with the repo policy installed — the
// minimum root a Manager can render configs from.
func nebPolicyRoot(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	nebInstallPolicy(tb, root)
	return root
}

// TestRepoPolicyLoads: the shipped file parses and validates. The
// semantic assertions on what it must and must not admit live with the
// render tests (nebconf_test, nebmachine_test), which now exercise the
// real file through the real pipeline.
func TestRepoPolicyLoads(t *testing.T) {
	p := nebRepoPolicy(t)
	for scope, rules := range map[string][]nebRuleYAML{
		"hub": p.Hub.Inbound, "node": p.Node.Inbound, "device": p.Device.Inbound,
	} {
		if len(rules) == 0 {
			t.Errorf("repo policy scope %q is empty", scope)
		}
	}
}

// TestLoadPolicyMissingFileFails: unlike the blocklist, no policy file
// is an error — an empty admission table renders members that drop
// everything.
func TestLoadPolicyMissingFileFails(t *testing.T) {
	if _, err := loadPolicy(t.TempDir()); err == nil {
		t.Error("expected an error for a missing policy file")
	}
}

func TestLoadPolicyRejectsInvalid(t *testing.T) {
	valid := `
hub:
  inbound:
    - {port: any, proto: icmp, host: any}
node:
  inbound:
    - {port: any, proto: icmp, host: any}
device:
  inbound:
    - {port: any, proto: icmp, host: any}
`
	tests := map[string]struct {
		mutate func(string) string // rewrite the valid doc
		want   string              // substring of the error
	}{
		"unknown group": {
			func(s string) string {
				return strings.Replace(s, "{port: any, proto: icmp, host: any}\nnode",
					"{port: \"80\", proto: tcp, group: mediia}\nnode", 1)
			},
			"not a cert group",
		},
		"both host and group": {
			func(s string) string {
				return strings.Replace(s, "{port: any, proto: icmp, host: any}\nnode",
					"{port: any, proto: any, host: any, group: admins}\nnode", 1)
			},
			"exactly one of host/group",
		},
		"neither host nor group": {
			func(s string) string {
				return strings.Replace(s, "{port: any, proto: icmp, host: any}\nnode",
					"{port: any, proto: any}\nnode", 1)
			},
			"exactly one of host/group",
		},
		"bad proto": {
			func(s string) string { return strings.Replace(s, "proto: icmp", "proto: tpc", 1) },
			"not any/tcp/udp/icmp",
		},
		"bad port": {
			func(s string) string {
				return strings.Replace(s, "port: any, proto: icmp, host: any}\nnode", "port: \"70000\", proto: icmp, host: any}\nnode", 1)
			},
			"1-65535",
		},
		"typoed key": {
			func(s string) string { return strings.Replace(s, "device:\n  inbound:", "device:\n  inboud:", 1) },
			"", // yaml strict-field error text is the library's
		},
		"empty scope": {
			func(s string) string {
				return strings.Replace(s, "device:\n  inbound:\n    - {port: any, proto: icmp, host: any}\n", "device:\n  inbound: []\n", 1)
			},
			"no inbound rules",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			doc := tc.mutate(valid)
			if doc == valid {
				t.Fatal("mutation did not apply — test bug")
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, PolicyFile), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := loadPolicy(root)
			if err == nil {
				t.Fatalf("expected an error for:\n%s", doc)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// nebOverlayDoc is a minimal valid policy whose scopes carry marker
// ports, so tests can tell an overlay-rendered config from a
// git-rendered one.
const nebOverlayDoc = `
hub:
  inbound:
    - {port: any, proto: icmp, host: any}
node:
  inbound:
    - {port: "18443", proto: tcp, group: admins}
device:
  inbound:
    - {port: "18080", proto: tcp, group: media}
`

// TestPolicyOverlayRoundTrip: set → effectivePolicy serves the
// overlay; clear → back to the git file. The overlay is memory-only by
// construction (no file is written), which is the invariant-2 property
// the whole phase leans on.
func TestPolicyOverlayRoundTrip(t *testing.T) {
	m, _ := testNebManager(t, t.TempDir())

	if _, _, _, ok := m.PolicyOverlay(); ok {
		t.Fatal("fresh manager reports an overlay")
	}
	base, err := m.effectivePolicy()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.SetPolicyOverlay([]byte(nebOverlayDoc), "0xabc"); err != nil {
		t.Fatal(err)
	}
	p, err := m.effectivePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Node.Inbound) != 1 || p.Node.Inbound[0].Port != "18443" {
		t.Errorf("effective node rules = %+v, want the overlay's single 18443 rule", p.Node.Inbound)
	}
	raw, by, since, ok := m.PolicyOverlay()
	if !ok || by != "0xabc" || string(raw) != nebOverlayDoc || since.IsZero() {
		t.Errorf("PolicyOverlay = (%q, %q, %v, %v)", raw, by, since, ok)
	}

	m.ClearPolicyOverlay()
	p, err = m.effectivePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Node.Inbound) != len(base.Node.Inbound) {
		t.Errorf("after clear: %d node rules, want the git file's %d", len(p.Node.Inbound), len(base.Node.Inbound))
	}
}

// TestPolicyOverlayRendersIntoDeviceConfig: the overlay flows through
// the real render pipeline, not just the accessor — a freshly enrolled
// device's firewall carries the overlay rule while it is installed.
func TestPolicyOverlayRendersIntoDeviceConfig(t *testing.T) {
	m, _ := testNebManager(t, t.TempDir())
	addr, err := nebderive.DeviceIP(nebTestMaster, "tv", nebSealSubnet)
	if err != nil {
		t.Fatal(err)
	}
	var pub [32]byte

	before, err := m.renderDeviceConfig(nebTestMaster, "tv", GroupMedia, addr, pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "18080") {
		t.Fatal("git-rendered device config already contains the marker port — test bug")
	}

	if err := m.SetPolicyOverlay([]byte(nebOverlayDoc), "0xabc"); err != nil {
		t.Fatal(err)
	}
	after, err := m.renderDeviceConfig(nebTestMaster, "tv", GroupMedia, addr, pub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "18080") {
		t.Error("overlay-rendered device config is missing the overlay's inbound rule")
	}

	m.ClearPolicyOverlay()
	reverted, err := m.renderDeviceConfig(nebTestMaster, "tv", GroupMedia, addr, pub)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reverted), "18080") {
		t.Error("cleared overlay still renders into device configs")
	}
}

// TestSetPolicyOverlayRejectsInvalid: a bad overlay is refused with the
// same validation as the git file, and a failed set never clobbers the
// installed overlay.
func TestSetPolicyOverlayRejectsInvalid(t *testing.T) {
	m, _ := testNebManager(t, t.TempDir())

	bad := strings.Replace(nebOverlayDoc, "group: media", "group: mediia", 1)
	if err := m.SetPolicyOverlay([]byte(bad), "0xabc"); err == nil || !strings.Contains(err.Error(), "not a cert group") {
		t.Errorf("bad overlay: err = %v, want a cert-group rejection", err)
	}
	if _, _, _, ok := m.PolicyOverlay(); ok {
		t.Error("rejected overlay was installed anyway")
	}

	if err := m.SetPolicyOverlay([]byte(nebOverlayDoc), "0xabc"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPolicyOverlay([]byte(bad), "0xdef"); err == nil {
		t.Fatal("bad overlay accepted")
	}
	if raw, by, _, ok := m.PolicyOverlay(); !ok || by != "0xabc" || string(raw) != nebOverlayDoc {
		t.Error("failed set clobbered the installed overlay")
	}
}
