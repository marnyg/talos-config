package main

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/marnyg/talos-config/config-server/deviceflow"
	"github.com/marnyg/talos-config/config-server/ethsig"
)

// wellKnownAddr is the Ethereum address of private key 0x...01 — an
// independent vector for keccak + address derivation.
const wellKnownAddr = "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"

func testKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	b := make([]byte, 32)
	b[31] = 1
	priv := secp256k1.PrivKeyFromBytes(b)
	return priv
}

// personalSign produces an Ethereum personal_sign signature (r||s||v hex).
func personalSign(t *testing.T, priv *secp256k1.PrivateKey, message string) string {
	t.Helper()
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := ethsig.Keccak256([]byte(prefixed))
	compact := secpecdsa.SignCompact(priv, hash, false) // [v+27 || r || s]
	sig := make([]byte, 65)
	copy(sig[:64], compact[1:])
	sig[64] = compact[0] // keep 27/28; RecoverPersonalSign handles both
	return "0x" + hex.EncodeToString(sig)
}

func TestHTTPSignatureApproval(t *testing.T) {
	s := newTestServer(t)
	s.adminToken = "" // wallet-only
	s.adminAddrs = []string{wellKnownAddr}
	ts := httptest.NewServer(s.mux())
	defer ts.Close()

	priv := testKey(t)
	da := s.store.Begin(deviceflow.KindMachine, "talos-pxe", map[string]string{"mac": "aa:bb:cc:dd:ee:ff"})

	post := func(action, sig string) int {
		resp, err := http.PostForm(ts.URL+"/verify", url.Values{
			"user_code": {da.UserCode}, "action": {action}, "signature": {sig},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Signature by a non-allowlisted key is rejected.
	b := make([]byte, 32)
	b[31] = 2
	otherPriv := secp256k1.PrivKeyFromBytes(b)
	msg := approvalMessage("approve", da.UserCode, da.Nonce)
	if code := post("approve", personalSign(t, otherPriv, msg)); code != http.StatusForbidden {
		t.Fatalf("wrong key: expected 403, got %d", code)
	}

	// Signature over the wrong action is rejected (message binds action).
	denyMsg := approvalMessage("deny", da.UserCode, da.Nonce)
	if code := post("approve", personalSign(t, priv, denyMsg)); code != http.StatusForbidden {
		t.Fatalf("action mismatch: expected 403, got %d", code)
	}

	// Correct signature approves.
	if code := post("approve", personalSign(t, priv, msg)); code != http.StatusOK {
		t.Fatalf("valid signature: expected 200, got %d", code)
	}

	// The machine's poll now yields a token.
	token, errCode := s.store.Poll(da.DeviceCode)
	if errCode != "" || token == "" {
		t.Fatalf("expected token after wallet approval, got err=%q", errCode)
	}

	// Admin token path is disabled when unset.
	da2 := s.store.Begin(deviceflow.KindMachine, "talos-pxe", nil)
	resp, err := http.PostForm(ts.URL+"/verify", url.Values{
		"user_code": {da2.UserCode}, "action": {"approve"}, "admin_token": {"anything"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token path with no token configured: expected 403, got %d", resp.StatusCode)
	}
}
