// Package policy is the Mesh v3 policy compiler (359.8.5; ADR-0017 and
// its 2026-09-18 amendment): it turns the Owner's recipe
// talos/mesh-policy-v3.yaml into the unsigned `invoke` grants a caller
// carries, and it is the shared vocabulary — kinds, the closed facet
// set per kind, ALPN(facet), AcceptTable(kind) — that the hub and every
// receiver agree on without exchanging a table.
//
// The package is pure: no I/O beyond Load, no clock reads, no
// signatures. Compile is deterministic in (recipe, caller, now); the
// Issuer signs its output at #bundle. Receivers hold none of this
// table — a receiver's `facet → forward` map is its own constant, and
// the only thing it shares with the hub is the vocabulary below.
//
// Kind ≡ facet vocabulary: facet names are disjoint across receiver
// kinds, so a grant's reach is its facet's, and its target is the
// wildcard "*" (protocol ADR-0004) — git cannot enumerate receiver keys
// (ADR-0015), and a "*" grant is honored at exactly the receivers that
// consented to the Owner for that facet. The recipe's node:/gateway:/hub:
// keys are validation structure (Nickel, verification/nickel/
// mesh-policy-v3.ncl), not compiled data.
package policy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/marnyg/talos-config/protocol/cert"
)

// File is the recipe's name under the talos tree, beside the frozen v2
// mesh-policy.yaml that the nebula render keeps reading until Phase 4.
const File = "mesh-policy-v3.yaml"

// BlocklistFile is the v3 blocklist beside File: one blocked member key
// (an ed: actor id — the iroh EndpointId a member cert names) per line.
// Sibling of the frozen v2 mesh-blocklist.txt (nebula cert
// fingerprints), which the nebula render keeps reading until Phase 4.
const BlocklistFile = "mesh-blocklist-v3.txt"

// GrantTTL is a compiled grant's lifetime: 7 days. Callers refetch on
// the renewal beat; the runway model (verification/quint/runway.qnt)
// bounds hub starvation at 6 days, one day inside this.
const GrantTTL int64 = 7 * 24 * 60 * 60

// Kind is a receiver kind — the actors that hold accept tables. A
// device only initiates and has no facets, so it is not a kind.
type Kind string

const (
	KindNode    Kind = "node"
	KindGateway Kind = "gateway"
	KindHub     Kind = "hub"
)

// Kinds is the closed receiver-kind set, in recipe order.
var Kinds = []Kind{KindNode, KindGateway, KindHub}

// Facets are the closed facet set per kind — the glossary's "Facet"
// entry verbatim, and the same table as mesh-policy-v3.ncl. Adding a
// facet is a domain-model change (glossary + ADR-0017), not an edit
// here. The iroh relay is not a facet; the Issuer's actor facets
// (#renew, mint-device) are granted by the Kit and enrollment, never by
// a recipe row.
var facets = map[Kind][]string{
	KindNode:    {"apid", "kube-api"},
	KindGateway: {"ingress-http", "jellyfin"},
	KindHub:     {"hub-http"},
}

// Groups is the closed group vocabulary. It mirrors mesh.Groups() (v2)
// and the Issuer's speak-as caveat: a grant addressed to a group the
// wallet never delegated would be a grant to nobody.
var Groups = []string{"admins", "media", "machines"}

// Facets returns the closed facet set of a kind (nil for an unknown
// kind). The returned slice is a copy.
func Facets(k Kind) []string { return slices.Clone(facets[k]) }

// KindOf returns the kind whose vocabulary owns facet, or "" if no kind
// does. Well-defined because facet names are disjoint across kinds.
func KindOf(facet string) Kind {
	for _, k := range Kinds {
		if slices.Contains(facets[k], facet) {
			return k
		}
	}
	return ""
}

// ALPN is the negotiated QUIC ALPN class for a facet:
// "talos-mesh/<facet>/v1". It is visible in the ClientHello, which is
// why facets are coarse classes and never ports.
func ALPN(facet string) string { return "talos-mesh/" + facet + "/v1" }

// AcceptTable is the ALPN → facet map a receiver of kind k hands to
// cert.Authorize (Input.AcceptTable). It is the inverse of ALPN over
// Facets(k) and nothing more: the forward address behind each facet is
// the receiver's own business. Nil for an unknown kind.
func AcceptTable(k Kind) map[string]string {
	fs, ok := facets[k]
	if !ok {
		return nil
	}
	t := make(map[string]string, len(fs))
	for _, f := range fs {
		t[ALPN(f)] = f
	}
	return t
}

// Rule is one recipe row: a facet plus exactly one of Host (a member
// name) or Group (a group name). Never a port.
type Rule struct {
	Facet string `yaml:"facet"`
	Host  string `yaml:"host,omitempty"`
	Group string `yaml:"group,omitempty"`
}

// Scope is one receiver kind's rows. `inbound: []` is legal and means
// "no grant names this kind".
type Scope struct {
	Inbound []Rule `yaml:"inbound"`
}

// Recipe is the parsed talos/mesh-policy-v3.yaml.
type Recipe struct {
	Node    Scope `yaml:"node"`
	Gateway Scope `yaml:"gateway"`
	Hub     Scope `yaml:"hub"`
}

// Scope returns the rows under kind k (nil for an unknown kind).
func (r Recipe) Scope(k Kind) []Rule {
	switch k {
	case KindNode:
		return r.Node.Inbound
	case KindGateway:
		return r.Gateway.Inbound
	case KindHub:
		return r.Hub.Inbound
	}
	return nil
}

// Load reads and validates <root>/mesh-policy-v3.yaml. A missing or
// malformed recipe is an error, never an empty default: the Issuer
// refusing to #bundle beats handing out grants nobody asked for.
func Load(root string) (Recipe, error) {
	raw, err := os.ReadFile(filepath.Join(root, File))
	if err != nil {
		return Recipe{}, fmt.Errorf("reading %s: %w", File, err)
	}
	r, err := Parse(raw)
	if err != nil {
		return Recipe{}, fmt.Errorf("%s: %w", File, err)
	}
	return r, nil
}

// LoadBlocklist reads <root>/mesh-blocklist-v3.txt: one ed: actor id per
// line, '#' comments and blank lines ignored, sorted and de-duplicated.
// A missing file is an empty list (nothing blocked yet); a malformed
// entry is an error, because silently skipping a typoed id would leave
// a blocked member served — the failure mode the file exists to
// prevent. The Issuer hands the list out on every #bundle (decision
// j0b): receivers replace their copy wholesale, a safe-to-lose cache.
func LoadBlocklist(root string) ([]cert.ActorID, error) {
	raw, err := os.ReadFile(filepath.Join(root, BlocklistFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", BlocklistFile, err)
	}
	return ParseBlocklist(raw)
}

// ParseBlocklist is LoadBlocklist over bytes.
func ParseBlocklist(raw []byte) ([]cert.ActorID, error) {
	var out []cert.ActorID
	for i, line := range strings.Split(string(raw), "\n") {
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id := cert.ActorID(line)
		if sch, err := id.Scheme(); err != nil || sch != "ed:" {
			return nil, fmt.Errorf("%s:%d: %q is not an ed: actor id", BlocklistFile, i+1, line)
		}
		if err := id.Validate(); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", BlocklistFile, i+1, err)
		}
		out = append(out, id)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Parse decodes and validates a recipe document. Strict fields: a typoed
// key ("inboud", "port") fails loudly instead of leaving a scope silently
// empty. Validate enforces the same closed sets as mesh-policy-v3.ncl.
func Parse(raw []byte) (Recipe, error) {
	var r Recipe
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return Recipe{}, fmt.Errorf("parsing: %w", err)
	}
	if err := r.Validate(); err != nil {
		return Recipe{}, err
	}
	return r, nil
}

// Validate checks a recipe against the closed vocabulary. It is the Go
// twin of the Nickel contracts (kept in step by TestVocabularyMatchesNickel):
// facet ∈ Facets(kind), exactly one of host/group, group ∈ Groups, host
// a member name (never "any"), and no `group: machines` row under node —
// machines must not reach each other's control surfaces
// (nebmachine_test stance).
func (r Recipe) Validate() error {
	for _, k := range Kinds {
		for i, rule := range r.Scope(k) {
			if err := validateRule(k, rule); err != nil {
				return fmt.Errorf("%s inbound[%d]: %w", k, i, err)
			}
		}
	}
	return nil
}

func validateRule(k Kind, rule Rule) error {
	if !slices.Contains(facets[k], rule.Facet) {
		return fmt.Errorf("%q is not a facet of a %s (closed set %v)", rule.Facet, k, facets[k])
	}
	if (rule.Host == "") == (rule.Group == "") {
		return fmt.Errorf("a rule names exactly one of host/group (the grant's aud)")
	}
	if rule.Host == "any" {
		return fmt.Errorf("host must be a member name; `any` is not an actor (ADR-0017)")
	}
	if rule.Group != "" && !slices.Contains(Groups, rule.Group) {
		return fmt.Errorf("unknown group %q (closed set %v)", rule.Group, Groups)
	}
	if k == KindNode && rule.Group == "machines" {
		return fmt.Errorf("node facets are never granted to `group: machines`")
	}
	return nil
}

// Caller is the identity Compile compiles for: the member cert's
// cav.name and cav.groups plus its aud (the member's own key). It is
// what the Issuer knows about the caller at #bundle from the member
// cert it verified — never anything the caller claimed in the request.
type Caller struct {
	Key    cert.ActorID
	Name   string
	Groups []string
}

// Allows is the three-line reference interpreter of a recipe (4un): the
// caller may reach facet f on a receiver of kind k iff some row under k
// names f and either the caller's name (host) or one of its groups.
// Compile must emit grants that admit exactly this relation — the
// round-trip law in policy_laws_test.go.
func (r Recipe) Allows(c Caller, k Kind, f string) bool {
	for _, rule := range r.Scope(k) {
		if rule.Facet == f && (rule.Host != "" && rule.Host == c.Name || rule.Group != "" && slices.Contains(c.Groups, rule.Group)) {
			return true
		}
	}
	return false
}

// Compile renders the grants the caller is entitled to: one unsigned
// `invoke` cert per recipe row that names the caller — by group (aud
// "group:<g>", g ∈ c.Groups) or by host (aud = the caller's key, host ==
// c.Name). Every grant has cav.target ["*"] (ADR-0004), cav.facet [the
// row's facet], delegable:false, iat now, exp now+GrantTTL. Iss is left
// empty; cert.Sign sets it to the Issuer's hot key.
//
// Rows the caller does not match compile to nothing: the caller carries
// only what the recipe asked for (4un's load-bearing direction). Output
// order is recipe order, so the result is deterministic and a bundle's
// bytes are a pure function of (recipe, caller, now).
func Compile(r Recipe, c Caller, now int64) []cert.Cert {
	var out []cert.Cert
	for _, k := range Kinds {
		for _, rule := range r.Scope(k) {
			var aud string
			switch {
			case rule.Group != "" && slices.Contains(c.Groups, rule.Group):
				aud = "group:" + rule.Group
			case rule.Host != "" && rule.Host == c.Name:
				aud = string(c.Key)
			default:
				continue
			}
			out = append(out, cert.Cert{
				Aud: aud,
				Can: cert.VerbInvoke,
				Cav: cert.Caveats{
					Target:    []cert.ActorID{cert.TargetAny},
					Facet:     []string{rule.Facet},
					Delegable: false,
				},
				Iat: now,
				Exp: now + GrantTTL,
			})
		}
	}
	return out
}
