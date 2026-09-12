# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Protocol M2 landed — absorb it, then choose M3 vs Phase 0.**
ADR-0001 is built 1:1: `authorize.qnt` ⇄ `protocol/cert.VerifyChain`,
`protocol/envelope`, `protocol/actor` (serial mailbox, `#renew`,
location cache), and `iroh-transport/` as its own module; acceptance
(two actors over in-memory + iroh) passes in `protocol/actor` and
`iroh-transport` tests. Immediate work is wrap-up: close the `0bc.2.x`
beads, read the first x86_64-linux `static` CI run (the Talos-extension
link probe), triage the workers' open questions into beads or rulings.
The next milestone is an owner call: `0bc.3` (M3 lighthouse on the fly
hub, postage for strangers) or the Mesh v3 Phase 0 probes `359.1.1–.3`
that gate `0bc.2.7` (two actors on real Talos nodes).

**Toward goal:** **Sovereign-actor protocol at the center** and
**Mesh v3** in `desired-state/goals.md` (ADR-0016, decision `5w1`);
protocol goals "one primitive" and "deployment-independent transport".

**Out of scope:**
- Nothing in the repo is protected (owner ruling 2026-09-06) — break
  nebula-era code where the new shape needs it.
- M3+ enforcement (postage, spawn, money) until M3 is chosen — M2 only
  makes `aud:*` fail closed without postage.
- The open numbers (chain cap, `max_bytes`, mailbox depth 64 is a
  placeholder, renewal fraction) — pick when a test forces it.
- Parents'-TV deployment (`4te`).
