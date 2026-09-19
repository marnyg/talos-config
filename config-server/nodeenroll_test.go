package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marnyg/talos-config/config-server/boottoken"
	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/issuer"
	"github.com/marnyg/talos-config/config-server/mesh"
	"github.com/marnyg/talos-config/config-server/nodeagent"
	"github.com/marnyg/talos-config/protocol/cert"
	"gopkg.in/yaml.v3"
)

// agentConfigFrom digs the p0agent document out of a served multi-doc
// machine config and parses its file — what Talos + the agent do.
func agentConfigFrom(t *testing.T, served string) nodeagent.Config {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(served))
	for {
		var doc struct {
			Kind        string `yaml:"kind"`
			Name        string `yaml:"name"`
			ConfigFiles []struct {
				Content   string `yaml:"content"`
				MountPath string `yaml:"mountPath"`
			} `yaml:"configFiles"`
		}
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("no %s ExtensionServiceConfig in served config: %v", nodeagent.Service, err)
		}
		if doc.Kind != "ExtensionServiceConfig" || doc.Name != nodeagent.Service {
			continue
		}
		if len(doc.ConfigFiles) != 1 || doc.ConfigFiles[0].MountPath != nodeagent.ConfigPath {
			t.Fatalf("agent document: %+v", doc)
		}
		var c nodeagent.Config
		if err := yaml.Unmarshal([]byte(doc.ConfigFiles[0].Content), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
}

func postEnroll(t *testing.T, s *server, node cert.ActorID, token string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(nodeagent.EnrollRequest{Node: node, Token: token})
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest("POST", nodeagent.EnrollPath, bytes.NewReader(body)))
	return rec
}

// TestNodeBootEnroll is ADR-0015 end to end on the hub: a served config
// carries a token instead of a key; the token buys exactly one member
// cert for a NodeId the hub never saw before, named by git.
func TestNodeBootEnroll(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	m.publicURL = "https://hub.example"
	s := &server{root: m.root, store: deviceflow.NewStore(), sessions: newSessionStore(), hub: m, adminAddrs: m.adminAddrs}
	_, nodePriv, _ := ed25519.GenerateKey(rand.Reader)
	node := cert.NewEdSigner(nodePriv).ActorID()

	// Sealed: no config, and no enrollment either.
	if rec := postEnroll(t, s, node, "bt1.x.y"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sealed enroll: %d", rec.Code)
	}
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/config: %d %s", rec.Code, rec.Body.String())
	}
	cfg := agentConfigFrom(t, rec.Body.String())
	if cfg.Hub != m.publicURL || cfg.Relay != m.publicURL || !strings.HasPrefix(cfg.Token, "bt1.") {
		t.Fatalf("agent config: %+v", cfg)
	}
	if mac, err := boottoken.Verify(m.current(), cfg.Token, time.Now()); err != nil || mac != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("served token: %q %v", mac, err)
	}
	// The served config carries no member key: only the token.
	if strings.Contains(rec.Body.String(), "PRIVATE KEY") && !strings.Contains(rec.Body.String(), "NEBULA") {
		t.Fatal("served config carries a private key")
	}

	// Master unsealed but the Issuer not: the token verifies, nothing
	// can be minted — 503, the agent retries.
	if rec := postEnroll(t, s, node, cfg.Token); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("issuer sealed: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := m.unsealIssuer(speakAsSig(t, m, testKey(t))); err != nil {
		t.Fatal(err)
	}

	rec = postEnroll(t, s, node, cfg.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: %d %s", rec.Code, rec.Body.String())
	}
	kit, err := issuer.DecodeKit(rec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	mem := kit.Member
	if mem.Aud != string(node) || mem.Iss != m.issuer.ID() || mem.Can != cert.VerbMember ||
		mem.Cav.Name != "aa-bb-cc-dd-ee-ff" || len(mem.Cav.Groups) != 1 || mem.Cav.Groups[0] != mesh.GroupMachines {
		t.Fatalf("member: %+v", mem)
	}
	if err := cert.Verify(mem); err != nil {
		t.Fatal(err)
	}
	if kit.BeatGrant.Aud != string(node) || kit.SpeakAs.Aud != string(m.issuer.ID()) {
		t.Fatalf("kit: %+v", kit)
	}

	// Single-use per process.
	if rec := postEnroll(t, s, node, cfg.Token); rec.Code != http.StatusConflict {
		t.Fatalf("replay: %d", rec.Code)
	}
	// A forged / foreign token.
	other, _ := boottoken.Mint(bytes.Repeat([]byte{1}, 32), "aa:bb:cc:dd:ee:ff", time.Now())
	if rec := postEnroll(t, s, node, other); rec.Code != http.StatusUnauthorized {
		t.Fatalf("foreign token: %d", rec.Code)
	}
	// A valid token for a MAC git does not declare.
	undeclared, _ := boottoken.Mint(m.current(), "11:22:33:44:55:66", time.Now())
	if rec := postEnroll(t, s, node, undeclared); rec.Code != http.StatusUnauthorized {
		t.Fatalf("undeclared mac: %d", rec.Code)
	}
	// A device id that is not an ed: key cannot be a node.
	if rec := postEnroll(t, s, issuer.WalletID(wellKnownAddr), cfg.Token); rec.Code == http.StatusOK {
		t.Fatal("eth id enrolled as a node")
	}
	// Every serve mints a fresh token.
	rec = httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil))
	if agentConfigFrom(t, rec.Body.String()).Token == cfg.Token {
		t.Fatal("two serves, one token")
	}
}

// Without an identity plane (no --iroh-relay) the served config is the
// v2 shape: no agent document at all.
func TestNoAgentDocumentWithoutRelay(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr}, "")
	s := &server{root: m.root, store: deviceflow.NewStore(), hub: m, adminAddrs: m.adminAddrs}
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "name: "+nodeagent.Service) {
		t.Fatalf("%d: %s", rec.Code, rec.Body.String())
	}
}
