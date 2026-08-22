package mesh

// Property-based law suite for policy composition (epic
// talos-config-7wg). Each property names the design law it pins:
//
//   L1 (ADR-0014): an installed overlay REPLACES the git policy
//       wholesale — never merges, never falls back per scope.
//   L2: parsePolicy is deterministic — same bytes, same table.
//
// Generators produce only *valid* policies (validatePolicyRule's
// domain); rejection of invalid ones is covered by nebpolicy_test.go.

import (
	"net/netip"
	"os"
	"path/filepath"
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

// TestPropOverlayReplaces: L1 through the real Manager path. Whatever
// policy git declares, installing an overlay makes effectivePolicy
// exactly the overlay's table (no rule from git survives), and
// clearing it restores exactly git's table.
func TestPropOverlayReplaces(t *testing.T) {
	subnet := netip.MustParsePrefix("10.42.0.0/16")
	rapid.Check(t, func(rt *rapid.T) {
		gitPol := genPolicyDoc().Draw(rt, "gitPolicy")
		ovlPol := genPolicyDoc().Draw(rt, "overlayPolicy")
		gitRaw, ovlRaw := marshalPolicy(rt, gitPol), marshalPolicy(rt, ovlPol)

		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, PolicyFile), gitRaw, 0o600); err != nil {
			rt.Fatalf("writing git policy: %v", err)
		}
		m := NewManager(4242, subnet, "0.0.0.0", "hub.example:4242", "", root)

		if err := m.SetPolicyOverlay(ovlRaw, "0xtest"); err != nil {
			rt.Fatalf("installing generated overlay: %v", err)
		}
		eff, err := m.effectivePolicy()
		if err != nil {
			rt.Fatalf("effectivePolicy with overlay: %v", err)
		}
		want, err := parsePolicy(ovlRaw)
		if err != nil {
			rt.Fatalf("re-parsing overlay: %v", err)
		}
		if !reflect.DeepEqual(eff, want) {
			rt.Fatalf("overlay did not replace wholesale:\n got %+v\nwant %+v", eff, want)
		}

		m.ClearPolicyOverlay()
		eff, err = m.effectivePolicy()
		if err != nil {
			rt.Fatalf("effectivePolicy after clear: %v", err)
		}
		want, err = parsePolicy(gitRaw)
		if err != nil {
			rt.Fatalf("re-parsing git policy: %v", err)
		}
		if !reflect.DeepEqual(eff, want) {
			rt.Fatalf("clear did not restore git policy:\n got %+v\nwant %+v", eff, want)
		}
	})
}

// TestPropComposeEffective: L1 on the pure core, all cases.
func TestPropComposeEffective(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		base := genPolicyDoc().Draw(rt, "base")
		overlay := genPolicyDoc().Draw(rt, "overlay")
		if got := composeEffective(base, overlay); got != overlay {
			rt.Fatalf("overlay installed: got %p, want overlay %p", got, overlay)
		}
		if got := composeEffective(base, nil); got != base {
			rt.Fatalf("no overlay: got %p, want base %p", got, base)
		}
	})
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
