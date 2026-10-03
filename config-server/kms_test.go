package main

import (
	"bytes"
	"context"
	"gopkg.in/yaml.v3"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kmsapi "github.com/siderolabs/kms-client/api/kms"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/masterderive"
)

const (
	declaredUUID   = "8c0d9a51-6e23-4ba1-a1d7-2d5d4c6b0f00"
	undeclaredUUID = "ffffffff-0000-1111-2222-333333333333"
)

// newTestKMS returns an unsealed manager + KMS server whose single
// machine declares declaredUUID.
func newTestKMS(t *testing.T) (*hubManager, *kmsServer) {
	t.Helper()
	m := testHubManager(t, []string{wellKnownAddr})
	meta := "config: base.yaml\npatches: []\nuuid: " + strings.ToUpper(declaredUUID) + "\n"
	if err := os.WriteFile(filepath.Join(m.root, "machines", "aa-bb-cc-dd-ee-ff", "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.unsealWithSignature(unsealSig(t)); err != nil {
		t.Fatal(err)
	}
	return m, newKMSServer(m.root, m)
}

func TestKMSSealUnsealRoundtrip(t *testing.T) {
	_, k := newTestKMS(t)
	diskKey := bytes.Repeat([]byte{0x42}, 32)

	sealed, err := k.Seal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: diskKey})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Data, diskKey) {
		t.Fatal("sealed blob contains the plaintext key")
	}

	out, err := k.Unseal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: sealed.Data})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Data, diskKey) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestKMSUnsealDeclaredSurvivesRestart(t *testing.T) {
	m, k := newTestKMS(t)
	sealed, err := k.Seal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: []byte("key")})
	if err != nil {
		t.Fatal(err)
	}

	// A fresh kmsServer models a server restart: no session state.
	restarted := newKMSServer(m.root, m)
	if _, err := restarted.Unseal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: sealed.Data}); err != nil {
		t.Fatalf("declared uuid must unseal after restart: %v", err)
	}
}

func TestKMSUndeclaredPolicy(t *testing.T) {
	m, k := newTestKMS(t)

	// Seal is open for undeclared UUIDs (fresh install).
	sealed, err := k.Seal(context.Background(), &kmsapi.Request{NodeUuid: undeclaredUUID, Data: []byte("key")})
	if err != nil {
		t.Fatalf("seal must be open: %v", err)
	}
	if got := k.undeclaredSealed(); len(got) != 1 || got[0] != undeclaredUUID {
		t.Fatalf("undeclaredSealed: got %v", got)
	}

	// Grace: same server lifetime unseals.
	if _, err := k.Unseal(context.Background(), &kmsapi.Request{NodeUuid: undeclaredUUID, Data: sealed.Data}); err != nil {
		t.Fatalf("session-sealed uuid must unseal: %v", err)
	}

	// After a restart the grace is gone: refused, even with a valid blob.
	restarted := newKMSServer(m.root, m)
	_, err = restarted.Unseal(context.Background(), &kmsapi.Request{NodeUuid: undeclaredUUID, Data: sealed.Data})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("undeclared uuid after restart: got %v, want PermissionDenied", err)
	}
}

func TestKMSUnsealRejectsForgedBlob(t *testing.T) {
	_, k := newTestKMS(t)
	sealed, err := k.Seal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: []byte("key")})
	if err != nil {
		t.Fatal(err)
	}

	tampered := append([]byte{}, sealed.Data...)
	tampered[len(tampered)-1] ^= 1
	if _, err := k.Unseal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: tampered}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("tampered blob: got %v, want PermissionDenied", err)
	}

	// A blob sealed for one UUID must not unseal under another
	// declared identity (per-UUID key separation) — declare a second
	// machine to get past the allowlist.
	if _, err := k.Unseal(context.Background(), &kmsapi.Request{NodeUuid: strings.ToUpper(declaredUUID), Data: sealed.Data}); err != nil {
		t.Fatalf("case-insensitive uuid must still unseal: %v", err)
	}
}

func TestKMSSealedServerRefuses(t *testing.T) {
	m := testHubManager(t, []string{wellKnownAddr})
	k := newKMSServer(m.root, m) // manager never unsealed

	_, err := k.Seal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: []byte("key")})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("sealed seal: got %v, want Unavailable", err)
	}
	_, err = k.Unseal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: []byte("blob")})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("sealed unseal: got %v, want Unavailable", err)
	}
}

func TestKMSMissingUUID(t *testing.T) {
	_, k := newTestKMS(t)
	if _, err := k.Seal(context.Background(), &kmsapi.Request{Data: []byte("key")}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("got %v, want InvalidArgument", err)
	}
}

// TestDiskEncryptionInjection: a machine with diskEncryption gets the
// systemDiskEncryption block with the KMS endpoint and the derived
// recovery passphrase; the WG-only output stays free of it.
func TestDiskEncryptionInjection(t *testing.T) {
	m, _ := newTestKMS(t)
	s := &server{root: m.root, store: deviceflow.NewStore(), hub: m, adminAddrs: m.adminAddrs, kmsAdvertise: "https://kms.example:443"}

	fetch := func() (int, string) {
		rec := httptest.NewRecorder()
		s.handleConfig(rec, httptest.NewRequest("GET", "/config?mac=aa-bb-cc-dd-ee-ff", nil))
		return rec.Code, rec.Body.String()
	}

	// Without diskEncryption: no encryption block.
	code, body := fetch()
	if code != http.StatusOK {
		t.Fatalf("got %d: %s", code, body)
	}
	if strings.Contains(body, "systemDiskEncryption") {
		t.Fatal("encryption injected without diskEncryption flag")
	}

	// Enable diskEncryption in meta.yaml.
	meta := "config: base.yaml\npatches: []\nuuid: " + declaredUUID + "\ndiskEncryption: true\n"
	if err := os.WriteFile(filepath.Join(m.root, "machines", "aa-bb-cc-dd-ee-ff", "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body = fetch()
	if code != http.StatusOK {
		t.Fatalf("got %d: %s", code, body)
	}
	wantPass := masterderive.RecoveryPassphrase(m.current(), declaredUUID)
	for _, want := range []string{"systemDiskEncryption", "https://kms.example:443", wantPass, "luks2"} {
		if !strings.Contains(body, want) {
			t.Errorf("composed config missing %q", want)
		}
	}
	if legacy := masterderive.RecoveryPassphraseMAC(m.current(), "aa:bb:cc:dd:ee:ff"); strings.Contains(body, legacy) {
		t.Error("composed config carries the v1 (MAC-derived) passphrase without installMAC")
	}

	// Without a UUID there is no durable handle to derive from: refuse,
	// rather than serve a passphrase keyed to the request's MAC.
	if err := os.WriteFile(filepath.Join(m.root, "machines", "aa-bb-cc-dd-ee-ff", "meta.yaml"), []byte("config: base.yaml\npatches: []\ndiskEncryption: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := fetch(); code != http.StatusInternalServerError {
		t.Fatalf("diskEncryption without uuid: got %d, want 500", code)
	}
	if err := os.WriteFile(filepath.Join(m.root, "machines", "aa-bb-cc-dd-ee-ff", "meta.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without an advertised KMS endpoint the config must be refused,
	// not silently served unencrypted.
	s.kmsAdvertise = ""
	if code, _ := fetch(); code != http.StatusInternalServerError {
		t.Fatalf("no kms endpoint: got %d, want 500", code)
	}
}

// TestKMSOverH2C exercises the real wire path: gRPC client →
// cleartext-h2 server → KMS service, sharing the port with plain HTTP
// (same Protocols setup as main).
func TestKMSOverH2C(t *testing.T) {
	m, k := newTestKMS(t)
	s := &server{root: m.root, store: deviceflow.NewStore(), hub: m, adminAddrs: m.adminAddrs, kms: k, kmsAdvertise: "grpc://ignored", sessions: newSessionStore()}

	ts := httptest.NewUnstartedServer(s.handler())
	ts.Config.Protocols = cleartextH2Protocols()
	ts.Start()
	defer ts.Close()

	// Plain HTTP/1.1 still works alongside cleartext h2.
	resp, err := http.Get(ts.URL + "/sealed")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/sealed via cleartext-h2 server: got %d", resp.StatusCode)
	}

	// gRPC over prior-knowledge cleartext h2.
	conn, err := grpcDialH2C(t, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := kmsapi.NewKMSServiceClient(conn)
	sealed, err := client.Seal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: []byte("disk-key")})
	if err != nil {
		t.Fatalf("gRPC seal: %v", err)
	}
	out, err := client.Unseal(context.Background(), &kmsapi.Request{NodeUuid: declaredUUID, Data: sealed.Data})
	if err != nil {
		t.Fatalf("gRPC unseal: %v", err)
	}
	if string(out.Data) != "disk-key" {
		t.Fatal("gRPC roundtrip mismatch")
	}
}

// grpcDialH2C dials a plaintext HTTP/2 gRPC connection to an httptest URL.
func grpcDialH2C(t *testing.T, url string) (*grpc.ClientConn, error) {
	t.Helper()
	return grpc.NewClient(strings.TrimPrefix(url, "http://"),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// TestDiskEncryptionSurvivesRename pins the property c4vd broke
// (2026-09): the encryption block served for a machine is a function
// of its declared identity, not of the directory the request selected.
// A NIC swap renames machines/<mac>/ — the LUKS header created at
// install does not follow, and Talos re-keys to a changed config only
// at boot, behind a slot-0 unlock ADR-0004 does not trust. So: same
// meta.yaml under a new directory → byte-identical systemDiskEncryption,
// for a UUID-derived install and for a grandfathered (installMAC) one.
func TestDiskEncryptionSurvivesRename(t *testing.T) {
	m, _ := newTestKMS(t)
	s := &server{root: m.root, store: deviceflow.NewStore(), hub: m, adminAddrs: m.adminAddrs, kmsAdvertise: "https://kms.example:443"}

	encryptionBlock := func(dirMAC string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		s.handleConfig(rec, httptest.NewRequest("GET", "/config?mac="+dirMAC, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d: %s", dirMAC, rec.Code, rec.Body.String())
		}
		var doc struct {
			Machine struct {
				SystemDiskEncryption yaml.Node `yaml:"systemDiskEncryption"`
			} `yaml:"machine"`
		}
		if err := yaml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("%s: %v", dirMAC, err)
		}
		if doc.Machine.SystemDiskEncryption.IsZero() {
			t.Fatalf("%s: no systemDiskEncryption block", dirMAC)
		}
		block, err := yaml.Marshal(&doc.Machine.SystemDiskEncryption)
		if err != nil {
			t.Fatal(err)
		}
		return string(block)
	}
	declare := func(dirMAC, meta string) {
		t.Helper()
		dir := filepath.Join(m.root, "machines", dirMAC)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name, meta string
		want       string
	}{
		{"uuid-derived", "config: base.yaml\npatches: []\nuuid: " + declaredUUID + "\ndiskEncryption: true\n",
			masterderive.RecoveryPassphrase(m.current(), declaredUUID)},
		{"grandfathered", "config: base.yaml\npatches: []\nuuid: " + declaredUUID + "\ndiskEncryption: true\ninstallMAC: aa:bb:cc:dd:ee:ff\n",
			masterderive.RecoveryPassphraseMAC(m.current(), "aa:bb:cc:dd:ee:ff")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declare("aa-bb-cc-dd-ee-ff", tc.meta)
			before := encryptionBlock("aa-bb-cc-dd-ee-ff")
			if !strings.Contains(before, tc.want) {
				t.Fatalf("passphrase: want %s in\n%s", tc.want, before)
			}
			// The NIC swap: same declaration, new directory.
			declare("11-22-33-44-55-66", tc.meta)
			after := encryptionBlock("11-22-33-44-55-66")
			if before != after {
				t.Fatalf("directory rename rotated the encryption block:\n--- before\n%s\n--- after\n%s", before, after)
			}
			if err := os.RemoveAll(filepath.Join(m.root, "machines", "11-22-33-44-55-66")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
