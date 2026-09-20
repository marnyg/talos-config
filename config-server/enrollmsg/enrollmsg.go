// Package enrollmsg is the canonical text an owner's wallet signs to
// enroll a mesh device (ADR-0012), shared by the WAN handlers that
// offer it, the /status page that rebuilds it live, and the Issuer that
// verifies it inside #mint-device (ADR-0024: what Enroll asks for
// carries the wallet's own proof).
//
// v3 binds (name, group, node, nonce): the device's NodeId (its
// Ed25519 actor id, the iroh EndpointId) is the identity the wallet
// admits, so the Issuer can check that the wallet — not Enroll — named
// it. v1 (nebula pubkey fingerprint) and v2 (fingerprint + node) died
// with the nebula plane in Mesh v3 Phase 4; a signature over either is
// no longer accepted. Distinct prefix from the machine approval, login
// and master-key messages: a signature for one must never replay as
// another.
package enrollmsg

import "fmt"

// V3 is the enrollment message: the device's actor id (ed:<hex>) as
// `node`, plus the final name and group the approver picked.
func V3(name, group, node, nonce string) string {
	return fmt.Sprintf(
		"talos config-server mesh device enrollment v3\nname: %s\ngroup: %s\nnode: %s\nnonce: %s",
		name, group, node, nonce,
	)
}
