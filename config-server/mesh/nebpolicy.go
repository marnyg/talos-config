package mesh

// Mesh access policy (task 6462fed4, phase 1): the who×what table —
// which cert identity may reach which port on which member class —
// declared in talos/mesh-policy.yaml instead of Go constants, following
// the blocklist pattern (nebblock.go). Git stays the single source of
// truth (invariant 2): changing who-reaches-what is a commit plus the
// already-routine propagation (redeploy the hub, re-apply the nodes,
// re-enroll devices), not a code change.
//
// Unlike the blocklist, a missing or malformed policy is an error, not
// an empty default: every config this server composes embeds these
// rules, and "no rules" renders members that drop everything. Refusing
// the derive beats serving a config that bricks the mesh — the same
// retry-over-wrong-config stance as serveTimePatches.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// PolicyFile lives beside machines/ under the talos tree, like the
// blocklist, so the hub image and any checkout agree on where policy
// is declared.
const PolicyFile = "mesh-policy.yaml"

// meshPolicy is the parsed policy: one inbound admission table per
// member class. Outbound stays open everywhere by design (members
// initiate; nebula's firewall is stateful), so the file declares
// inbound only.
type meshPolicy struct {
	Hub    policyScope `yaml:"hub"`
	Node   policyScope `yaml:"node"`
	Device policyScope `yaml:"device"`
}

type policyScope struct {
	Inbound []nebRuleYAML `yaml:"inbound"`
}

// policyGroups is the closed set of cert groups a rule may name. The
// firewall matches groups signed into peer certificates, so a group
// unknown to enrollment would silently admit nobody — rejected at load
// for the same reason the blocklist rejects malformed fingerprints.
var policyGroups = map[string]bool{
	GroupAdmins:   true,
	GroupMedia:    true,
	GroupMachines: true,
}

// loadPolicy reads and validates talos/mesh-policy.yaml.
func loadPolicy(root string) (*meshPolicy, error) {
	raw, err := os.ReadFile(filepath.Join(root, PolicyFile))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", PolicyFile, err)
	}
	p, err := parsePolicy(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", PolicyFile, err)
	}
	return p, nil
}

// parsePolicy parses and validates a policy document. Shared by the
// git file (loadPolicy) and the ephemeral overlay (SetPolicyOverlay):
// the overlay must clear exactly the bar the file does — a typo must
// not brick composed members just because it arrived over HTTP instead
// of a commit.
func parsePolicy(raw []byte) (*meshPolicy, error) {
	var p meshPolicy
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// Strict fields: a typoed key ("inboud") must fail loudly, not
	// leave a scope silently empty.
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parsing: %w", err)
	}
	for scope, rules := range map[string][]nebRuleYAML{
		"hub":    p.Hub.Inbound,
		"node":   p.Node.Inbound,
		"device": p.Device.Inbound,
	} {
		if len(rules) == 0 {
			return nil, fmt.Errorf("scope %q declares no inbound rules — a member class that drops everything (even ICMP) is almost certainly a mistake", scope)
		}
		for i, r := range rules {
			if err := validatePolicyRule(r); err != nil {
				return nil, fmt.Errorf("%s inbound[%d]: %w", scope, i, err)
			}
		}
	}
	return &p, nil
}

// --- Ephemeral policy overlay (task 6462fed4, phase 2) ---
//
// The overlay is a full replacement policy document held in memory
// only: a redeploy or restart drops it, so git remains the only
// durable owner of policy (invariant 2). It exists for the experiment
// loop — install a candidate table, exercise it through the normal
// propagation channels (device re-enrollment now, node configs on the
// next apply), then export the exact text into talos/mesh-policy.yaml
// and commit. The hub's OWN firewall renders at unseal, which in
// practice precedes any overlay in this process's lifetime, so the hub
// scope effectively always rides git until live sync (phases 3–4).

type policyOverlay struct {
	raw    []byte
	parsed *meshPolicy
	by     string // wallet address that signed the install
	since  time.Time
}

// SetPolicyOverlay validates raw and installs it as the effective
// policy. by is the wallet address whose signature authorized it,
// recorded for the /policy page only — the signature itself was
// verified by the caller.
func (m *Manager) SetPolicyOverlay(raw []byte, by string) error {
	p, err := parsePolicy(raw)
	if err != nil {
		return err
	}
	m.polMu.Lock()
	defer m.polMu.Unlock()
	m.polOver = &policyOverlay{raw: raw, parsed: p, by: by, since: time.Now()}
	return nil
}

// ClearPolicyOverlay reverts the effective policy to the git file.
func (m *Manager) ClearPolicyOverlay() {
	m.polMu.Lock()
	defer m.polMu.Unlock()
	m.polOver = nil
}

// PolicyOverlay reports the installed overlay, if any.
func (m *Manager) PolicyOverlay() (raw []byte, by string, since time.Time, ok bool) {
	m.polMu.Lock()
	defer m.polMu.Unlock()
	if m.polOver == nil {
		return nil, "", time.Time{}, false
	}
	return m.polOver.raw, m.polOver.by, m.polOver.since, true
}

// PolicyGitRaw returns the policy file as shipped in this hub's talos
// tree — the base every diff on the /policy page is against.
func (m *Manager) PolicyGitRaw() ([]byte, error) {
	return os.ReadFile(filepath.Join(m.root, PolicyFile))
}

// effectivePolicy is what every render site composes with: the overlay
// when one is installed, the git file otherwise.
func (m *Manager) effectivePolicy() (*meshPolicy, error) {
	m.polMu.Lock()
	o := m.polOver
	m.polMu.Unlock()
	if o != nil {
		return o.parsed, nil
	}
	return loadPolicy(m.root)
}

func validatePolicyRule(r nebRuleYAML) error {
	switch r.Proto {
	case "any", "tcp", "udp", "icmp":
	default:
		return fmt.Errorf("proto %q is not any/tcp/udp/icmp", r.Proto)
	}
	if r.Port != "any" {
		n, err := strconv.Atoi(r.Port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port %q is not %q or 1-65535", r.Port, "any")
		}
	}
	if (r.Host == "") == (r.Group == "") {
		return fmt.Errorf("exactly one of host/group must be set (host=%q group=%q)", r.Host, r.Group)
	}
	if r.Group != "" && !policyGroups[r.Group] {
		return fmt.Errorf("group %q is not a cert group this mesh issues (admins/media/machines)", r.Group)
	}
	return nil
}
