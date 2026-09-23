# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M4 — spawn as funded enrollment — is being built.**
M4.1 landed 2026-09-23 (`protocol/spawn`: birth consent, intro,
`#spawn` client, nonce table, `#birth` → kit, promise, `Born`).
Next is `0bc.4.2` — the provisioner *actor* (`#spawn`/`#extend`/
`#kill`, lease machine, `Driver` seam) and the `#renew` decorator —
gated on thread `6sax` (a renewed root consent must be re-installed).
Then drivers (k8s Job, docker), the child binary, the acceptance run
(`0bc.4.3–.6`). Design: ADR-0008/0009 Proposed, invariant 13.
M1–M3 are built and closed.

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
