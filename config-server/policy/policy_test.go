package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/marnyg/talos-config/protocol/cert"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// The shipped recipe loads, and it is the amended shape: no relay row,
// no machines row under node, hub is hub-http only.
func TestLoadShippedRecipe(t *testing.T) {
	r, err := Load(filepath.Join(repoRoot(t), "talos"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range Kinds {
		for _, rule := range r.Scope(k) {
			if rule.Facet == "relay" {
				t.Fatalf("%s: relay row survived — the relay is not a facet (ADR-0017 amendment)", k)
			}
		}
	}
	if !r.Allows(Caller{Name: "laptop", Groups: []string{"admins"}}, KindNode, "apid") {
		t.Fatal("admins should reach node apid")
	}
	if r.Allows(Caller{Name: "cp1", Groups: []string{"machines"}}, KindNode, "apid") {
		t.Fatal("machines must not reach node apid")
	}
	if r.Allows(Caller{Name: "tv", Groups: []string{"media"}}, KindHub, "hub-http") != true {
		t.Fatal("media should reach hub-http")
	}
}

// The compiled Default is the shipped recipe's `facets:` section: the
// device side (irohup defaults, the bridge guard) still reads Default,
// so until it reads the vocabulary off the plane the two must not drift.
func TestDefaultMatchesShippedRecipe(t *testing.T) {
	r, err := Load(filepath.Join(repoRoot(t), "talos"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Facets) == 0 {
		t.Fatal("shipped recipe has no facets: section")
	}
	if !slices.Equal(r.Facets, Default) {
		t.Fatalf("talos/mesh-policy-v3.yaml facets:\n%v\n≠ policy.Default\n%v", r.Facets, Default)
	}
	if !slices.Equal(r.Vocabulary(), Default) {
		t.Fatal("Vocabulary() should be the declared section")
	}
}

// The shipped yaml, mutated the way check.sh mutates it for the Nickel
// contract (mutants 20–27 + the positive control), gets the same
// verdict from Parse: a facets: row with a bad kind/port/key, a name
// twice, a port twice within a kind, a rule naming an undeclared facet
// all fail; a brand-new facet plus a rule naming it passes with no Go
// change (the acceptance of ADR-0048).
func TestShippedRecipeMutants(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "talos", File))
	if err != nil {
		t.Fatal(err)
	}
	live := string(raw)
	addFacet := func(row string) string {
		return strings.Replace(live, "facets:\n", "facets:\n  - "+row+"\n", 1)
	}
	mustDiffer := func(name, doc string) {
		t.Helper()
		if doc == live {
			t.Fatalf("%s: mutation did not apply", name)
		}
	}
	blame := map[string]string{
		"21 facet kind phone":            addFacet("{name: probe, kind: phone, port: 4242}"),
		"22 facet port 0":                addFacet("{name: probe, kind: gateway, port: 0}"),
		"23 facet port 70000":            addFacet("{name: probe, kind: gateway, port: 70000}"),
		"24 facet row proto key":         addFacet("{name: probe, kind: gateway, port: 4242, proto: tcp}"),
		"25 facet name twice":            addFacet("{name: ssh, kind: node, port: 2222}"),
		"26 port twice within a kind":    addFacet("{name: probe, kind: gateway, port: 3389}"),
		"27 rule names undeclared facet": strings.Replace(live, "  - {name: rdp, kind: gateway, port: 3389}\n", "", 1),
		"empty facet name":               addFacet("{name: \"\", kind: gateway, port: 4242}"),
	}
	for name, doc := range blame {
		mustDiffer(name, doc)
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: parsed, want error", name)
		}
	}
	probe := strings.Replace(addFacet("{name: probe, kind: gateway, port: 4242}"),
		"    - {facet: rdp, group: admins}\n", "    - {facet: rdp, group: admins}\n    - {facet: probe, group: admins}\n", 1)
	mustDiffer("probe", probe)
	r, err := Parse([]byte(probe))
	if err != nil {
		t.Fatalf("a new facet is a data change: %v", err)
	}
	if r.Vocabulary().KindOf("probe") != KindGateway || r.Vocabulary().Port("probe") != 4242 || KindOf("probe") != "" {
		t.Fatal("probe should be in the recipe's vocabulary and not in Default")
	}
	if !r.Allows(Caller{Name: "laptop", Groups: []string{"admins"}}, KindGateway, "probe") {
		t.Fatal("admins should reach gateway probe")
	}
	if gs := Compile(r, Caller{Key: cert.ActorID("ed:" + strings.Repeat("ab", 32)), Name: "laptop", Groups: []string{"admins"}}, 1); !slices.ContainsFunc(gs, func(g cert.Cert) bool { return g.Cav.Facet[0] == "probe" }) {
		t.Fatal("probe grant not compiled")
	}
	// Mutant 20 (section omitted) is not a Parse error: an old checkout
	// or a test fixture without `facets:` reads Default. The Nickel
	// contract requires the section in the shipped file.
	noSection := strings.Replace(live, live[strings.Index(live, "facets:\n"):strings.Index(live, "node:\n")], "", 1)
	mustDiffer("20", noSection)
	r, err = Parse([]byte(noSection))
	if err != nil {
		t.Fatalf("no facets: section should fall back to Default: %v", err)
	}
	if len(r.Facets) != 0 || !slices.Equal(r.Vocabulary(), Default) {
		t.Fatal("Vocabulary() should be Default without a section")
	}
	// The Default is validated like a section.
	if err := Default.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Vocabulary{{"a", KindNode, 1}, {"a", KindHub, 2}}).Validate(); err == nil {
		t.Fatal("duplicate names across kinds should fail")
	}
	if err := (Vocabulary{{"a", KindNode, 80}, {"b", KindHub, 80}}).Validate(); err != nil {
		t.Fatalf("same port across kinds is legal: %v", err)
	}
}

// The group vocabulary is still a compiled constant on both sides.
func TestGroupsMatchNickel(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "verification", "nickel", "mesh-policy-v3.ncl"))
	if err != nil {
		t.Fatal(err)
	}
	groups := regexp.MustCompile(`let groups = \[([^\]]*)\]`).FindSubmatch(raw)
	if groups == nil || strings.ReplaceAll(strings.ReplaceAll(string(groups[1]), `"`, ""), " ", "") != strings.Join(Groups, ",") {
		t.Fatalf("nickel groups %q ≠ Go %v", groups, Groups)
	}
	kinds := regexp.MustCompile(`let kinds = \[([^\]]*)\]`).FindSubmatch(raw)
	want := make([]string, len(Kinds))
	for i, k := range Kinds {
		want[i] = string(k)
	}
	if kinds == nil || strings.ReplaceAll(strings.ReplaceAll(string(kinds[1]), `"`, ""), " ", "") != strings.Join(want, ",") {
		t.Fatalf("nickel kinds %q ≠ Go %v", kinds, Kinds)
	}
}

func TestFacetsDisjointAcrossKinds(t *testing.T) {
	seen := map[string]Kind{}
	for _, k := range Kinds {
		for _, f := range Facets(k) {
			if prev, dup := seen[f]; dup {
				t.Fatalf("facet %q in both %s and %s — kind ≡ facet vocabulary needs disjoint names", f, prev, k)
			}
			seen[f] = k
			if KindOf(f) != k {
				t.Fatalf("KindOf(%q) = %q, want %q", f, KindOf(f), k)
			}
		}
	}
	if KindOf("relay") != "" || KindOf("") != "" {
		t.Fatal("relay / empty must not be facets")
	}
}

func TestAcceptTable(t *testing.T) {
	at := AcceptTable(KindNode)
	if at["talos-mesh/apid/v1"] != "apid" || at["talos-mesh/kube-api/v1"] != "kube-api" || len(at) != 2 {
		t.Fatalf("node accept table = %v", at)
	}
	if _, ok := at[ALPN("ingress-http")]; ok {
		t.Fatal("a node must not accept a gateway facet's ALPN")
	}
	if at := AcceptTable(KindDevice); at[ALPN("ssh")] != "ssh" || len(at) != 1 {
		t.Fatalf("device accept table = %v", at)
	}
	if AcceptTable("phone") != nil {
		t.Fatal("phone is not a kind")
	}
}

// Parse mirrors the Nickel mutants (check.sh 8–19) so a recipe that the
// contract would blame never reaches Compile either.
func TestParseRejects(t *testing.T) {
	ok := "node: {inbound: [{facet: apid, group: admins}]}\ngateway: {inbound: []}\nhub: {inbound: []}\n"
	if _, err := Parse([]byte(ok)); err != nil {
		t.Fatalf("baseline should parse: %v", err)
	}
	cases := map[string]string{
		"unknown kind":        ok + "phone: {inbound: []}\n",
		"facet of other kind": strings.Replace(ok, "facet: apid", "facet: ingress-http", 1),
		"relay facet":         strings.Replace(ok, "facet: apid, group: admins}]}\ngateway", "facet: apid, group: admins}]}\nhub: {inbound: [{facet: relay, group: admins}]}\ngateway", 1),
		"port key":            strings.Replace(ok, "group: admins}", "group: admins, port: \"80\"}", 1),
		"host and group":      strings.Replace(ok, "group: admins}", "group: admins, host: laptop}", 1),
		"neither":             strings.Replace(ok, ", group: admins", "", 1),
		"typoed group":        strings.Replace(ok, "admins", "admin", 1),
		"host any":            strings.Replace(ok, "group: admins", "host: any", 1),
		"machines on node":    strings.Replace(ok, "admins", "machines", 1),
		"typoed key":          strings.Replace(ok, "inbound: [{facet", "inboud: [{facet", 1),
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: parsed, want error\n%s", name, doc)
		}
	}
	// Positive controls (check.sh): empty scopes and a host row.
	if _, err := Parse([]byte("node: {inbound: [{facet: apid, host: hub}]}\ngateway: {inbound: []}\nhub: {inbound: []}\n")); err != nil {
		t.Fatalf("host row should parse: %v", err)
	}
}

func TestCompileExample(t *testing.T) {
	r, err := Parse([]byte(`
node:
  inbound:
    - {facet: apid, group: admins}
    - {facet: kube-api, host: laptop}
gateway:
  inbound:
    - {facet: ingress-http, group: media}
hub:
  inbound: []
`))
	if err != nil {
		t.Fatal(err)
	}
	key := cert.ActorID("ed:" + strings.Repeat("ab", 32))
	gs := Compile(r, Caller{Key: key, Name: "laptop", Groups: []string{"admins"}}, 1000)
	if len(gs) != 2 {
		t.Fatalf("got %d grants, want 2 (apid by group, kube-api by host; media row skipped): %+v", len(gs), gs)
	}
	if gs[0].Aud != "group:admins" || gs[0].Cav.Facet[0] != "apid" {
		t.Fatalf("grant 0 = %+v", gs[0])
	}
	if gs[1].Aud != string(key) || gs[1].Cav.Facet[0] != "kube-api" {
		t.Fatalf("grant 1 = %+v", gs[1])
	}
	for _, g := range gs {
		if g.Can != cert.VerbInvoke || !cert.IsTargetAny(g.Cav.Target) || g.Cav.Delegable || g.Iat != 1000 || g.Exp != 1000+GrantTTL || g.Iss != "" || g.Sig != nil {
			t.Fatalf("grant shape: %+v", g)
		}
	}
	if got := Compile(r, Caller{Key: key, Name: "tv", Groups: []string{"machines"}}, 1000); len(got) != 0 {
		t.Fatalf("a machine matches no row here, got %+v", got)
	}
}

// The shipped v3 blocklist loads (empty today), and the parser is
// strict: ed: ids only, comments and blanks skipped, duplicates folded,
// a typo is an error rather than a silently unblocked member.
func TestBlocklist(t *testing.T) {
	if bl, err := LoadBlocklist(filepath.Join(repoRoot(t), "talos")); err != nil || len(bl) != 0 {
		t.Fatalf("shipped blocklist: %v %v", bl, err)
	}
	if bl, err := LoadBlocklist(t.TempDir()); err != nil || bl != nil {
		t.Fatalf("missing file should be an empty list: %v %v", bl, err)
	}
	a := "ed:" + strings.Repeat("ab", 32)
	b := "ed:" + strings.Repeat("cd", 32)
	bl, err := ParseBlocklist([]byte("# header\n" + b + "  # tv\n\n" + a + "\n" + b + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(bl, []cert.ActorID{cert.ActorID(a), cert.ActorID(b)}) {
		t.Fatalf("got %v", bl)
	}
	for _, bad := range []string{"eth:0x" + strings.Repeat("ab", 20), "ed:" + strings.Repeat("ab", 31), strings.Repeat("ab", 32), "ed:" + strings.Repeat("AB", 32)} {
		if _, err := ParseBlocklist([]byte(bad + "\n")); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// Every facet of every kind has a natural port (the presentations
// depend on it), ports are disjoint within a kind (a port is read in
// the vocabulary of the name's kind, so hub-http and ingress-http may
// both be 80), and FacetByPort inverts FacetPort within a kind.
func TestFacetPorts(t *testing.T) {
	for _, k := range Kinds {
		seen := map[uint16]string{}
		for _, f := range Facets(k) {
			p := FacetPort(f)
			if p == 0 {
				t.Errorf("%s facet %q has no natural port", k, f)
				continue
			}
			if other, dup := seen[p]; dup {
				t.Errorf("port %d shared by %q and %q", p, other, f)
			}
			seen[p] = f
			if got := FacetByPort(k, p); got != f {
				t.Errorf("FacetByPort(%s, %d) = %q, want %q", k, p, got, f)
			}
		}
	}
	if FacetByPort(KindNode, FacetPort("hub-http")) != "" || FacetByPort(KindNode, FacetPort("ingress-http")) != "" {
		t.Error("a hub or gateway port must not resolve to a node facet")
	}
	if FacetByPort(KindGateway, 80) != "ingress-http" || FacetByPort(KindHub, 80) != "hub-http" {
		t.Error("port 80 is read in the vocabulary of the kind")
	}
	if FacetByPort(KindNode, 0) != "" || FacetByPort(KindNode, 1) != "" || FacetPort("nope") != 0 {
		t.Error("unknown port/facet must map to zero values")
	}
}
