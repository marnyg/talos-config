// Package policy is the Mesh v3 policy compiler (359.8.5; ADR-0017 and
// its 2026-09-18 amendment): it turns the Owner's recipe
// talos/mesh-policy-v3.yaml into the unsigned `invoke` grants a caller
// carries. The recipe also carries the facet vocabulary — `facets:`
// rows of {name, kind, port} (ADR-0048) — so a new facet is a recipe
// edit, not a hub rebuild; the compiled Default below is what a member
// with no recipe at hand (a device, a test) falls back to.
//
// The package is pure: no I/O beyond Load, no clock reads, no
// signatures. Compile is deterministic in (recipe, caller, now); the
// Issuer signs its output at #bundle. Receivers hold none of this
// table — a receiver's accept table is exactly the facets it chose to
// forward or serve (nodeagent.Start), named in the recipe or not.
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

// File is the recipe's name under the talos tree. (The "-v3" is
// historical: it sat beside the v2 mesh-policy.yaml through the dual
// plane; renaming it would touch every deployed hub image for nothing.)
const File = "mesh-policy-v3.yaml"

// BlocklistFile is the v3 blocklist beside File: one blocked member key
// (an ed: actor id — the iroh EndpointId a member cert names) per line.
const BlocklistFile = "mesh-blocklist-v3.txt"

// GrantTTL is a compiled grant's lifetime: 7 days. Callers refetch on
// the renewal beat; the runway model (verification/quint/runway.qnt)
// bounds hub starvation at 6 days, one day inside this.
const GrantTTL int64 = 7 * 24 * 60 * 60

// Kind is a receiver kind — a facet vocabulary, not a kind of member
// (glossary "Facet": kind ≡ facet vocabulary). A device that serves
// nothing is not a receiver and reads as a node (zone.KindOf); one
// that serves a device facet (ssh, ADR-0038) is a receiver of kind
// device. The authorization is the same as for any kind: a grant
// addressed to a group or a principal, honored where the receiver
// consented — the kind only says which facet names are legal.
type Kind string

const (
	KindNode    Kind = "node"
	KindGateway Kind = "gateway"
	KindHub     Kind = "hub"
	KindDevice  Kind = "device"
)

// Kinds is the closed receiver-kind set, in recipe order.
var Kinds = []Kind{KindNode, KindGateway, KindHub, KindDevice}

// Facet is one row of the vocabulary: a named entry point a receiver
// of Kind may expose, and its natural Port — where its service listens
// on the receiver, and therefore the port a presentation shows for it
// (a bridge's default listener, the tun's <fake IP>:<port>).
type Facet struct {
	Name string `yaml:"name"`
	Kind Kind   `yaml:"kind"`
	Port uint16 `yaml:"port"`
}

// Vocabulary is the facet set — the recipe's `facets:` section (ADR-0048)
// or Default. Names are disjoint across kinds (kind ≡ facet vocabulary:
// a grant's reach is its facet's) and ports are disjoint within a kind
// (a port is read in the vocabulary of the name's kind, so hub-http and
// ingress-http may both be 80). The iroh relay is not a facet; the
// Issuer's actor facets (#renew, mint-device) are granted by the Kit
// and enrollment, never by a recipe row.
type Vocabulary []Facet

// Default is the compiled vocabulary: what a member reads when it has
// no recipe — a device's irohup defaults and bridge guard, a node
// agent's natural-port lookup, tests. It is NOT what the hub validates
// a recipe against when the recipe declares `facets:`; a facet missing
// here is still a facet there. Keep it equal to the shipped recipe's
// section (TestDefaultMatchesShippedRecipe) until the device side reads
// the vocabulary off the plane.
var Default = Vocabulary{
	{"apid", KindNode, 50000},
	{"kube-api", KindNode, 6443},
	{"ingress-http", KindGateway, 80},
	// Raw TCP to the win2k25 guest's RDP; the guest's login is the
	// second gate.
	{"rdp", KindGateway, 3389},
	// `syncthing` (ADR-0046): raw TCP to the cluster's Syncthing peer,
	// the always-on member of every device's sync set; Syncthing's
	// own device-ID handshake is the second gate (as rdp/ssh).
	{"syncthing", KindGateway, 22000},
	{"hub-http", KindHub, 80},
	// A device's own sshd (ADR-0038): raw TCP, the mesh grant is the
	// first gate and sshd's own authentication the second (as rdp).
	{"ssh", KindDevice, 22},
}

// Validate is the vocabulary's own closed-set check, the Go twin of the
// Nickel FacetTable contract: kind ∈ Kinds, a non-empty name, a
// non-zero port, names disjoint across the whole table, ports disjoint
// within a kind.
func (v Vocabulary) Validate() error {
	names := map[string]int{}
	ports := map[Kind]map[uint16]string{}
	for i, f := range v {
		if f.Name == "" {
			return fmt.Errorf("facets[%d]: empty name", i)
		}
		if !slices.Contains(Kinds, f.Kind) {
			return fmt.Errorf("facets[%d] %q: kind %q is not a receiver kind (closed set %v)", i, f.Name, f.Kind, Kinds)
		}
		if f.Port == 0 {
			return fmt.Errorf("facets[%d] %q: a facet has a natural port (1–65535)", i, f.Name)
		}
		if j, dup := names[f.Name]; dup {
			return fmt.Errorf("facets[%d] %q: already declared at facets[%d] (names are disjoint across kinds)", i, f.Name, j)
		}
		names[f.Name] = i
		if ports[f.Kind] == nil {
			ports[f.Kind] = map[uint16]string{}
		}
		if other, dup := ports[f.Kind][f.Port]; dup {
			return fmt.Errorf("facets[%d] %q: port %d already taken by %s facet %q", i, f.Name, f.Port, f.Kind, other)
		}
		ports[f.Kind][f.Port] = f.Name
	}
	return nil
}

// Facets returns the facet names of kind k in table order (nil for an
// unknown kind or a kind with no facets).
func (v Vocabulary) Facets(k Kind) []string {
	var out []string
	for _, f := range v {
		if f.Kind == k {
			out = append(out, f.Name)
		}
	}
	return out
}

// KindOf returns the kind whose vocabulary owns facet, or "" if none.
func (v Vocabulary) KindOf(facet string) Kind {
	for _, f := range v {
		if f.Name == facet {
			return f.Kind
		}
	}
	return ""
}

// Port returns the natural port of facet, or 0 if the table lacks it.
func (v Vocabulary) Port(facet string) uint16 {
	for _, f := range v {
		if f.Name == facet {
			return f.Port
		}
	}
	return 0
}

// ByPort is the inverse of Port within kind k: the facet of k whose
// natural port is port, or "" if none.
func (v Vocabulary) ByPort(k Kind, port uint16) string {
	if port == 0 {
		return ""
	}
	for _, f := range v {
		if f.Kind == k && f.Port == port {
			return f.Name
		}
	}
	return ""
}

// AcceptTable is the ALPN → facet map over Facets(k): what a receiver
// that served every facet of its kind would hand cert.Authorize. A real
// receiver's table is the one nodeagent.Start builds from what it
// actually forwards and serves. Nil for an unknown kind.
func (v Vocabulary) AcceptTable(k Kind) map[string]string {
	if !slices.Contains(Kinds, k) {
		return nil
	}
	t := map[string]string{}
	for _, f := range v.Facets(k) {
		t[ALPN(f)] = f
	}
	return t
}

// FacetPort is Default.Port: the natural port of facet, or 0.
func FacetPort(facet string) uint16 { return Default.Port(facet) }

// FacetByPort is Default.ByPort.
func FacetByPort(k Kind, port uint16) string { return Default.ByPort(k, port) }

// The closed group vocabulary, by name. Devices enroll as admins or
// media (the approver picks); machines are the node agents.
const (
	GroupAdmins   = "admins"
	GroupMedia    = "media"
	GroupMachines = "machines"
)

// Groups is the closed group vocabulary. It is the Issuer's speak-as
// caveat and the Nickel contracts assert against it: a grant addressed
// to a group the wallet never delegated would be a grant to nobody.
var Groups = []string{GroupAdmins, GroupMedia, GroupMachines}

// DeviceGroup reports whether g is a group a device may enroll into.
func DeviceGroup(g string) bool { return g == GroupAdmins || g == GroupMedia }

// Facets is Default.Facets: the compiled facet names of a kind.
func Facets(k Kind) []string { return Default.Facets(k) }

// KindOf is Default.KindOf.
func KindOf(facet string) Kind { return Default.KindOf(facet) }

// ALPN is the negotiated QUIC ALPN class for a facet:
// "talos-mesh/<facet>/v1". It is visible in the ClientHello, which is
// why facets are coarse classes and never ports.
func ALPN(facet string) string { return "talos-mesh/" + facet + "/v1" }

// AcceptTable is Default.AcceptTable: the inverse of ALPN over
// Facets(k). The hub's own facets (hubfacet.go) are read here — the hub
// is its own producer and deployer — and so are tests; a deployed
// receiver builds its table from its forward/serve set instead.
func AcceptTable(k Kind) map[string]string { return Default.AcceptTable(k) }

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

// Recipe is the parsed talos/mesh-policy-v3.yaml. A kind absent from
// the document is an empty scope (`inbound: []`): no grant names it.
// Facets is the `facets:` section; absent (an old checkout, a test
// fixture) ⇒ Vocabulary() is Default.
type Recipe struct {
	Facets  Vocabulary `yaml:"facets,omitempty"`
	Node    Scope      `yaml:"node"`
	Gateway Scope      `yaml:"gateway"`
	Hub     Scope      `yaml:"hub"`
	Device  Scope      `yaml:"device"`
}

// Vocabulary is the facet set this recipe's rows are read against: its
// own `facets:` section when it has one, else Default.
func (r Recipe) Vocabulary() Vocabulary {
	if len(r.Facets) == 0 {
		return Default
	}
	return r.Facets
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
	case KindDevice:
		return r.Device.Inbound
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

// Validate checks a recipe against its vocabulary. It is the Go twin of
// the Nickel contracts (kept in step by TestShippedRecipeMutants over
// the live yaml): the `facets:` section well-formed (Vocabulary.Validate),
// facet ∈ Vocabulary().Facets(kind), exactly one of host/group, group ∈
// Groups, host a member name (never "any"), and no `group: machines`
// row under node — machines must not reach each other's control
// surfaces (nebmachine_test stance).
func (r Recipe) Validate() error {
	v := r.Vocabulary()
	if err := v.Validate(); err != nil {
		return err
	}
	for _, k := range Kinds {
		for i, rule := range r.Scope(k) {
			if err := validateRule(v, k, rule); err != nil {
				return fmt.Errorf("%s inbound[%d]: %w", k, i, err)
			}
		}
	}
	return nil
}

func validateRule(v Vocabulary, k Kind, rule Rule) error {
	if known := v.Facets(k); !slices.Contains(known, rule.Facet) {
		return fmt.Errorf("%q is not a facet of a %s (declared %v)", rule.Facet, k, known)
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
