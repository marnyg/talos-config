# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M4 — spawn as funded enrollment — is designed
(2026-09-24, ADR-0008/0009 Proposed, invariant 13) and next to
build** (`0bc.4.1–.6`). Shape: a spawner library on the parent, a
provisioner *actor* (`#spawn`/`#extend`/`#kill`) that knows leases and
never learns about birth, a per-platform driver behind it (k8s Job +
docker in v0), a per-spawn `#birth` consent, passive leases extended
on the `#renew` beat, children that self-lapse. M1–M3 are built and
closed (cert, envelope/actor, lighthouse + postage).

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
