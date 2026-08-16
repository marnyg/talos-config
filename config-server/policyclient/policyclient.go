// Package policyclient is the device side of live policy sync (task
// 6462fed4, phase 3): fetching the hub's GET /policy document (the
// device scope of the effective mesh policy, epoch-tagged) and splicing
// its inbound rules into a device's nebula config. Shared by
// mobile.Tunnel (in-process ReloadConfigString) and nebup (rewrite the
// cached yml + SIGHUP the nebula child) so the two runners cannot drift
// on the wire format or the splice semantics.
//
// Policy is payload, not identity: a sync touches only
// firewall.inbound. Certs, keys and addresses never move (sketch
// 6462fed4's key insight — re-enrollment stays a 90-day ceremony, not a
// policy propagation channel).
package policyclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"
)

// Rule is one inbound admission rule, in both wire (JSON) and nebula
// config (YAML) shape — the same field set as the hub's
// mesh.FirewallRule; the e2e test in package mesh guards the two
// against drift.
type Rule struct {
	Port  string `json:"port" yaml:"port"`
	Proto string `json:"proto" yaml:"proto"`
	Host  string `json:"host,omitempty" yaml:"host,omitempty"`
	Group string `json:"group,omitempty" yaml:"group,omitempty"`
}

// Wire is the GET /policy response body.
type Wire struct {
	// Epoch identifies the rule set; clients compare it to the last
	// applied value and skip the reload when unchanged. Opaque to
	// clients (the hub computes it as a hash of the rules).
	Epoch   string `json:"epoch"`
	Inbound []Rule `json:"inbound"`
}

// HTTPClient is the one http.Client policy fetches share: short
// timeout, because a sealed hub (every fly deploy) makes this endpoint
// unreachable and a sync must fail fast and stay quiet, never wedge a
// caller.
var HTTPClient = &http.Client{Timeout: 10 * time.Second}

// Fetch retrieves the policy document from base (e.g.
// "http://10.42.0.1"). The route is mesh-only, so the request rides
// the device's own tunnel — reachability doubles as membership.
func Fetch(client *http.Client, base string) (*Wire, error) {
	resp, err := client.Get(base + "/policy")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /policy: %s", resp.Status)
	}
	var w Wire
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, fmt.Errorf("GET /policy: decoding: %w", err)
	}
	if w.Epoch == "" || len(w.Inbound) == 0 {
		// An empty rule set renders a device that drops everything —
		// the same refusal the hub's own policy loader makes.
		return nil, fmt.Errorf("GET /policy: empty epoch or rules")
	}
	return &w, nil
}

// InboundRules reads the firewall.inbound rules a config currently
// carries, for semantic before/after comparison — byte comparison
// would false-positive on the hub-rendered vs re-marshaled key order.
func InboundRules(cfgYAML []byte) ([]Rule, error) {
	var doc struct {
		Firewall struct {
			Inbound []Rule `yaml:"inbound"`
		} `yaml:"firewall"`
	}
	if err := yaml.Unmarshal(cfgYAML, &doc); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return doc.Firewall.Inbound, nil
}

// SpliceInbound returns cfgYAML with firewall.inbound replaced by
// rules. Everything else — pki (multi-line PEM blocks included),
// addresses, lighthouse, tun pins — round-trips through yaml.v3
// verbatim in value, and the result is parse-verified against the
// mutated document before it is trusted: a splice that would mangle
// the config returns an error and the caller keeps running on the old
// rules, which is always safe (stale policy, intact tunnel).
func SpliceInbound(cfgYAML []byte, rules []Rule) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(cfgYAML, &doc); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	fw, ok := doc["firewall"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("config has no firewall block")
	}
	inbound := make([]any, 0, len(rules))
	for _, r := range rules {
		m := map[string]any{"port": r.Port, "proto": r.Proto}
		if r.Host != "" {
			m["host"] = r.Host
		}
		if r.Group != "" {
			m["group"] = r.Group
		}
		inbound = append(inbound, m)
	}
	fw["inbound"] = inbound

	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("rendering config: %w", err)
	}
	// Parse-verify: the rendered text must read back as exactly the
	// document we meant to write (pinTunDev's stance, applied to the
	// whole config because the splice touched a map-typed section).
	var check map[string]any
	if err := yaml.Unmarshal(out, &check); err != nil {
		return nil, fmt.Errorf("spliced config failed to parse back: %w", err)
	}
	if !reflect.DeepEqual(doc, check) {
		return nil, fmt.Errorf("spliced config did not round-trip")
	}
	return out, nil
}
