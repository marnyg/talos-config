package mesh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
