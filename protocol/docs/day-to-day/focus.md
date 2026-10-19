# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M4 done; the lighthouse runs live (`0bc.6`, 2026-09-29).**
The M4.6 acceptance run passed on both drivers (birth, one `#renew →
#extend`, lapse; ADR-0008/0009 Accepted, invariant 13 held), and the
provisioner is now found by `#lookup` at `actors/cmd/lighthouse`
instead of a copied `location.json`: `Actor.Bootstrap` is the network
bundle's raw endpoints (ADR-0007 § Run live), which on iroh is the
lighthouse's id alone. Accepted on docker and on k8s; the
`sap-lighthouse` Deployment is live and the in-cluster provisioner
publishes to it. M1–M4 built plus discovery. **2026-10-12:** the first actor↔actor
stream facet (`agent-events`, chain-on-connect admitted by
`VerifyChain`) and `protocol/facethttp` landed for the agent child
(root ADR-0037) with no protocol change. **2026-10-13:** root rotation
(ADR-0010 Proposed, `rotation.qnt`, `cert/rotation.go`) — the wallet
root becomes recoverable by a guardian-signed epoch chain, inv 2/10
verbatim. **2026-10-15:** pins wired into authorization (ADR-0011,
`d0o0`): `Receiver.Pins`, subtractive resolution hop, hub + members
hold trivial pins, `#bundle` carries rotations; **2026-10-19** the
durable copy in `talos/pins/` (closes `d0o0` on deploy). **Next is the
owner's pick:** M5 money (`0bc.5`), or the parent actor (N=2 consumer,
live since 2026-10-18 in root scope).

**Toward goal:** `desired-state/goals.md` — *Sovereign identity with a
survivable compromise story* (root capture is now bounded by the
identity's own policy, not "the one unrecoverable event"), *One
primitive* (rotation is k certs of the same shape), *Spawning as funded
enrollment*.

**Out of scope:**
- M5 money: `payment`, tranches, the provisioner market (frontdoor /
  negotiated offers to `#spawn`).
- TEE attestation in the birth message; provider impersonation stays
  the stated v0 hole.
- Splitting the talos Provisioner along ADR-0009 (`kckm`); replacing
  the Phase-1 lighthouse view.
- Lighthouse lookup-cap in the intro (`lm2a`); the founder indirection
  (`-founder`) on the lighthouse binary.
- Address derivation that commits to the epoch-0 policy (CREATE2
  parameters), EIP-712 typed rotation bodies, the on-chain follower
  contract — all behind `4mg` / M5.
