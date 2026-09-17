// Package enrollmsg is the canonical text an owner's wallet signs to
// enroll a mesh device (ADR-0012), shared by the WAN handlers that
// offer it, the /status page that rebuilds it live, and the Issuer that
// verifies it inside #mint-device (ADR-0024: what Enroll asks for
// carries the wallet's own proof).
//
// Two versions coexist during the Mesh v3 dual plane:
//
//   - v1 binds (name, group, nebula pubkey fingerprint, nonce) — a
//     nebula-only enrollment. Deployed clients (nebup, the Android app)
//     speak this.
//   - v2 adds the device's NodeId (its Ed25519 actor id, the iroh
//     EndpointId). One signature then admits the device to both planes:
//     the nebula cert and the member cert are minted from the same
//     approval, and the Issuer can check that the wallet — not Enroll —
//     named the NodeId.
//
// Which version an enrollment uses is decided by whether the device
// presented a node id; the hub accepts both until Phase 4 deletes v1
// with nebula. Distinct prefixes from the wg enrollment, machine
// approval, login and master-key messages: a signature for one must
// never replay as another.
package enrollmsg

import "fmt"

// V1 is the nebula-only enrollment message.
func V1(name, group, fingerprint, nonce string) string {
	return fmt.Sprintf(
		"talos config-server mesh device enrollment v1\nname: %s\ngroup: %s\npubkey: %s\nnonce: %s",
		name, group, fingerprint, nonce,
	)
}

// V2 is the dual-plane enrollment message: v1's fields plus the
// device's actor id (ed:<hex>) as `node`.
func V2(name, group, fingerprint, node, nonce string) string {
	return fmt.Sprintf(
		"talos config-server mesh device enrollment v2\nname: %s\ngroup: %s\npubkey: %s\nnode: %s\nnonce: %s",
		name, group, fingerprint, node, nonce,
	)
}

// For picks the version by whether a node id is present.
func For(name, group, fingerprint, node, nonce string) string {
	if node == "" {
		return V1(name, group, fingerprint, nonce)
	}
	return V2(name, group, fingerprint, node, nonce)
}
