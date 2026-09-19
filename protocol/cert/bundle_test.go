package cert

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

func TestBundleRoundTrip(t *testing.T) {
	_, hub, _ := ed25519.GenerateKey(nil)
	_, node, _ := ed25519.GenerateKey(nil)
	hubS, nodeS := NewEdSigner(hub), NewEdSigner(node)
	member, err := Sign(Cert{Aud: string(nodeS.ActorID()), Can: VerbMember,
		Cav: Caveats{Name: "laptop", Groups: []string{"admins"}}, Iat: 1, Exp: 100}, hubS)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := Sign(Cert{Aud: "group:admins", Can: VerbInvoke,
		Cav: Caveats{Target: []ActorID{TargetAny}, Facet: []string{"apid"}}, Iat: 1, Exp: 100}, hubS)
	if err != nil {
		t.Fatal(err)
	}
	sa, err := Sign(Cert{Aud: string(hubS.ActorID()), Can: VerbSpeakAs,
		Cav: Caveats{Verbs: []string{"member", "invoke"}, Groups: []string{"admins"}}, Iat: 1, Exp: 100}, nodeS)
	if err != nil {
		t.Fatal(err)
	}
	in := Bundle{Member: member, Grants: []Cert{grant}, SpeakAs: []Cert{sa}}
	raw, err := EncodeBundle(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append([]Cert{out.Member}, append(out.Grants, out.SpeakAs...)...) {
		if err := Verify(c); err != nil {
			t.Fatalf("decoded cert does not verify: %v", err)
		}
	}
	if out.Member.Cav.Name != "laptop" || len(out.Grants) != 1 || len(out.SpeakAs) != 1 || out.Grants[0].Cav.Facet[0] != "apid" {
		t.Fatalf("round trip: %+v", out)
	}

	// Empty slices stay empty on the wire, never null.
	raw, _ = EncodeBundle(Bundle{Member: member})
	if !strings.Contains(string(raw), `"grants":[]`) || !strings.Contains(string(raw), `"speak_as":[]`) {
		t.Fatalf("nil slices should encode as []: %s", raw)
	}
	if _, err := DecodeBundle(raw); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeBundleRejects(t *testing.T) {
	_, hub, _ := ed25519.GenerateKey(nil)
	hubS := NewEdSigner(hub)
	grant, _ := Sign(Cert{Aud: "group:admins", Can: VerbInvoke,
		Cav: Caveats{Target: []ActorID{TargetAny}, Facet: []string{"apid"}}, Iat: 1, Exp: 100}, hubS)
	g, _ := Encode(grant)
	cases := map[string]string{
		"no member":         `{"grants":[],"speak_as":[]}`,
		"unknown key":       `{"member":` + string(g) + `,"grants":[],"speak_as":[],"extra":1}`,
		"member not verb":   `{"member":` + string(g) + `,"grants":[],"speak_as":[]}`,
		"trailing":          `{"member":` + string(g) + `,"grants":[],"speak_as":[]}{}`,
		"grant unknown cav": `{"member":` + string(g) + `,"grants":[{"iss":"ed:00","aud":"*","can":"invoke","cav":{"rate":1},"iat":1,"exp":2,"sig":""}],"speak_as":[]}`,
	}
	for name, raw := range cases {
		if _, err := DecodeBundle([]byte(raw)); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
}
