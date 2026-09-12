# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M2 build, Quint first.** M1 (`cert`, `clock`) is pinned to
its models. M2's shape is ruled in ADR-0001; the next code change is
`verification/quint/authorize.qnt` gaining the N-link chain
(`0bc.2.1`), then the Go port (`0bc.2.2`), then `envelope/` (`.3`),
`actor/` with an in-memory `Transport` (`.5`), and the iroh adapter as
its own module (`.6`). Acceptance: two actors exchanging capability
invocations in one Go test over both transports.

**Toward goal:** `desired-state/goals.md` — *One primitive* (no second
verifier), *Offline, receiver-rooted authorization* (N-link chains
rooted at the receiver), *Deployment-independent transport* (envelope
self-authenticating; `Transport` is an interface).

**Out of scope:**
- M3–M5: lighthouse, postage enforcement, spawn, money. M2 only makes
  `aud:*` without postage fail closed.
- Real nodes: `0bc.2.7` is deferred on the Talos extension probe.
- Persistence, supervision, store-and-forward (invariant 12).
