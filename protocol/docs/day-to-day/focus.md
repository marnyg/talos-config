# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M4 — spawn as funded enrollment — is being built.**
M4.1 landed 2026-09-23 (`protocol/spawn`: birth consent, intro,
`#spawn` client, nonce table, `#birth` → kit, promise, `Born`);
M4.2 the same day (`protocol/provisioner`: the provisioner *actor*,
lease machine, `Driver` seam; the spawner's `#renew` decorator and
`Kill`). 2026-09-25: restart re-adopts from platform labels
(`Adopt`, decision `uzgl`) and **both drivers are built** in the new
`actors/` module (`driver/k8s`, `driver/docker`; `0bc.4.3/.4`
closed), each verified against its live platform; **the same day
the child and provisioner binaries, the child's beat and the image
recipe** (`actors/child`, `actors/cmd/*`, `actors-image`; `0bc.4.5`).
What is left: push the image, then the acceptance run (`0bc.4.6`)
with a parent CLI. Design: ADR-0008/0009 Proposed (+
amendment), invariant 13. M1–M3 are built and closed.

**Toward goal:** `desired-state/goals.md` — *Spawning as funded
enrollment* ("let it crash" = "let the lease lapse"; a parent's death
costs a child one edge), *Sovereign identity* (private key born on
the child's compute), *One primitive* (birth, leases and extension are
facets and chains, no new authority path).

**Out of scope:**
- M5 money: `payment`, tranches, the provisioner market (frontdoor /
  negotiated offers to `#spawn`).
- TEE attestation in the birth message; provider impersonation stays
  the stated v0 hole.
- Splitting the talos Provisioner along ADR-0009 (`kckm`); replacing
  the Phase-1 lighthouse view.
- Lighthouse lookup-cap in the intro (`lm2a`).
