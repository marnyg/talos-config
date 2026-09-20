package mesh

// Property-based law suite for policy parsing (epic
// talos-config-7wg). L1 (ADR-0014's overlay-replaces-wholesale) left
// with the ephemeral overlay (ri3b); what remains:
//
//   L2: parsePolicy is deterministic — same bytes, same table.
//
// Generators produce only *valid* policies (validatePolicyRule's
// domain); rejection of invalid ones is covered by nebpolicy_test.go.

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"
)

func genPolicyRule() *rapid.Generator[nebRuleYAML] {
	return rapid.Custom(func(t *rapid.T) nebRuleYAML {
		r := nebRuleYAML{
			Proto: rapid.SampledFrom([]string{"any", "tcp", "udp", "icmp"}).Draw(t, "proto"),
		}
		if rapid.Bool().Draw(t, "anyPort") {
			r.Port = "any"
		} else {
			r.Port = rapid.SampledFrom([]string{"22", "80", "443", "8096", "65535", "1"}).Draw(t, "port")
		}
		if rapid.Bool().Draw(t, "byGroup") {
			r.Group = rapid.SampledFrom([]string{GroupAdmins, GroupMedia, GroupMachines}).Draw(t, "group")
		} else {
			r.Host = rapid.StringMatching(`[a-z][a-z0-9-]{0,11}`).Draw(t, "host")
		}
		return r
	})
}

func genPolicyDoc() *rapid.Generator[*meshPolicy] {
	rules := rapid.SliceOfN(genPolicyRule(), 1, 4)
	return rapid.Custom(func(t *rapid.T) *meshPolicy {
		return &meshPolicy{
			Hub:    policyScope{Inbound: rules.Draw(t, "hub")},
			Node:   policyScope{Inbound: rules.Draw(t, "node")},
			Device: policyScope{Inbound: rules.Draw(t, "device")},
		}
	})
}

// failer is the intersection of *testing.T and *rapid.T we need: inside
// a property, failures must go through *rapid.T so rapid can attribute
// and shrink the failing draw.
type failer interface {
	Fatalf(format string, args ...any)
}

func marshalPolicy(f failer, p *meshPolicy) []byte {
	raw, err := yaml.Marshal(p)
	if err != nil {
		f.Fatalf("marshaling generated policy: %v", err)
	}
	return raw
}

// TestPropParsePolicyDeterministic: L2.
func TestPropParsePolicyDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		raw := marshalPolicy(rt, genPolicyDoc().Draw(rt, "policy"))
		a, errA := parsePolicy(raw)
		b, errB := parsePolicy(raw)
		if (errA == nil) != (errB == nil) {
			rt.Fatalf("nondeterministic error: %v vs %v", errA, errB)
		}
		if !reflect.DeepEqual(a, b) {
			rt.Fatalf("nondeterministic parse:\n a %+v\n b %+v", a, b)
		}
	})
}
