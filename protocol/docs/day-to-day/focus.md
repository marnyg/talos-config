# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M4 done; the lighthouse runs live (`0bc.6`, 2026-09-29).**
The M4.6 acceptance run passed on both drivers (birth, one `#renew →
#extend`, lapse; ADR-0008/0009 Accepted, invariant 13 held), and the
provisioner is now found by `#lookup` at `actors/cmd/lighthouse`
instead of a copied `location.json`: `Actor.Bootstrap` is the network
bundle's raw endpoints (ADR-0007 § Run live), which on iroh is the
lighthouse's id alone. Accepted on docker; the k8s Deployment
(`k8s/apps/sap-lighthouse`) lands with the next image push and its
two-step id dance. M1–M4 built plus discovery. **Next is the owner's
pick:** M5 money (`0bc.5`) or the k8s lighthouse cut-over.

**Toward goal:** `desired-state/goals.md` — *Spawning as funded
enrollment*, *Sovereign identity* (private key born on the child's
compute), *One primitive* (birth, leases, extension and now discovery
are facets and chains; a bootstrap hint authorizes nothing).

**Out of scope:**
- M5 money: `payment`, tranches, the provisioner market (frontdoor /
  negotiated offers to `#spawn`).
- TEE attestation in the birth message; provider impersonation stays
  the stated v0 hole.
- Splitting the talos Provisioner along ADR-0009 (`kckm`); replacing
  the Phase-1 lighthouse view.
- Lighthouse lookup-cap in the intro (`lm2a`); the founder indirection
  (`-founder`) on the lighthouse binary.
