# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M2 landed; harden and rule before M3.** `cert`, `clock`,
`envelope`, `actor` are pinned to `authorize.qnt`/`clock.qnt` (the
model leads; Go follows 1:1). Two actors invoke capabilities over the
in-memory transport (`protocol/actor`) and over iroh
(`iroh-transport/`) in ordinary `go test`. The mechanical hardening
(`verified` on reject, `ErrPostageConflict`, self-guarded `clock.Mark`,
stale comments) landed 2026-09-13; what remains are **rulings**
(`xwu` verb uniformity, `0lo` absent `endpoints`, `7ei`/`7w5`/`5yj`)
that M3's `reach-me-at` and frontdoor paths will hit first, and the
still-unread musl link probe (`cs3`). M3 (`0bc.3`: lighthouse as a plain actor, PoW postage for
strangers) starts when the owner picks it over the Mesh v3 Phase 0
probes.

**Toward goal:** `desired-state/goals.md` — *One primitive* (one
verifier, now real), *Offline, receiver-rooted authorization*,
*Deployment-independent transport* (`Transport` interface; iroh is an
adapter module that `protocol/` never imports).

**Out of scope:**
- M3–M5 enforcement: lighthouse, postage checking, spawn, money.
- Real nodes: `0bc.2.7` is deferred on the Talos extension probe.
- Persistence, supervision, store-and-forward (invariant 12).
- Windowed/out-of-order `seq` — per-edge serial `Send` is v0.
