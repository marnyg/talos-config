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

// The Go vocabulary and the Nickel contract are the same table. The
// .ncl is the checked-in source the CI nickel job validates the recipe
// against; this test reads its `let facets = {...}` block so the two
// cannot drift apart silently.
func TestVocabularyMatchesNickel(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "verification", "nickel", "mesh-policy-v3.ncl"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)let facets = \{(.*?)\}\nin`).FindSubmatch(raw)
	if block == nil {
		t.Fatal("mesh-policy-v3.ncl: `let facets = {...}` block not found — update this test with the contract")
	}
	row := regexp.MustCompile(`(\w+) = \[([^\]]*)\]`)
	got := map[Kind][]string{}
	for _, m := range row.FindAllStringSubmatch(string(block[1]), -1) {
		var fs []string
		for _, q := range strings.Split(m[2], ",") {
			if q = strings.Trim(strings.TrimSpace(q), `"`); q != "" {
				fs = append(fs, q)
			}
		}
		got[Kind(m[1])] = fs
	}
	if len(got) != len(Kinds) {
		t.Fatalf("nickel kinds %v ≠ Go kinds %v", got, Kinds)
	}
	for _, k := range Kinds {
		if !slices.Equal(got[k], Facets(k)) {
			t.Fatalf("%s facets: nickel %v ≠ Go %v", k, got[k], Facets(k))
		}
	}
	groups := regexp.MustCompile(`let groups = \[([^\]]*)\]`).FindSubmatch(raw)
	if groups == nil || strings.ReplaceAll(strings.ReplaceAll(string(groups[1]), `"`, ""), " ", "") != strings.Join(Groups, ",") {
		t.Fatalf("nickel groups %q ≠ Go %v", groups, Groups)
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
	if AcceptTable("device") != nil {
		t.Fatal("device is not a kind")
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
		"unknown kind":        ok + "device: {inbound: []}\n",
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
