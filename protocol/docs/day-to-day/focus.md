# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M4 — spawn as funded enrollment — is done.** The
acceptance run (`0bc.4.6`, 2026-09-29) passed live over iroh on both
drivers: a laptop parent (`actors/cmd/spawn`) → an in-cluster k8s
provisioner and a docker one, same image; birth, one `#renew →
#extend`, lapse on both. ADR-0008/0009 Accepted, invariant 13 held.
M1–M4 built. **Next is the owner's pick:** M5 money (`0bc.5`) or
provisioner discovery through the lighthouse (replaces the
`location.json` file).

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
