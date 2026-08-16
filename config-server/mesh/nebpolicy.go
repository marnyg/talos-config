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
	var p meshPolicy
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// Strict fields: a typoed key ("inboud") must fail loudly, not
	// leave a scope silently empty.
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", PolicyFile, err)
	}
	for scope, rules := range map[string][]nebRuleYAML{
		"hub":    p.Hub.Inbound,
		"node":   p.Node.Inbound,
		"device": p.Device.Inbound,
	} {
		if len(rules) == 0 {
			return nil, fmt.Errorf("%s: scope %q declares no inbound rules — a member class that drops everything (even ICMP) is almost certainly a mistake", PolicyFile, scope)
		}
		for i, r := range rules {
			if err := validatePolicyRule(r); err != nil {
				return nil, fmt.Errorf("%s: %s inbound[%d]: %w", PolicyFile, scope, i, err)
			}
		}
	}
	return &p, nil
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
