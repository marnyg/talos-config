# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M2 is ruled and hardened; the protocol waits on the Mesh v3
Phase 0 gate before M3.** `cert`, `clock`, `envelope`, `actor` are
pinned to `authorize.qnt`/`clock.qnt` (model leads, Go follows 1:1);
two actors invoke over the in-memory transport and over iroh in
ordinary `go test`; the musl link is proven. The M2 threads were ruled
2026-09-13 (protocol ADR-0002: verb from the root consent, absent set
caveats are ∅; decisions `eak zey 5qt seb`). The one pre-M3 code item
is `xwu` (generalise the fold's verb, model first); `7w5`/`7ei` are
small runtime fixes. M3 (`0bc.3`: lighthouse as a plain actor, PoW
postage for strangers) starts after the gate `359.1.5` — its relay and
lighthouse land on the same hub relay P0.1 just proved (root ADR-0022).

**Toward goal:** `desired-state/goals.md` — *One primitive* (one
verifier for every verb), *Offline, receiver-rooted authorization*,
*Deployment-independent transport* (`Transport` interface; iroh is an
adapter module that `protocol/` never imports).

**Out of scope:**
- M3–M5 enforcement: lighthouse, postage checking, spawn, money.
- Real nodes: `0bc.2.7` is deferred on the Talos extension probe `359.1.3`.
- Persistence, supervision, store-and-forward (invariant 12).
- Windowed/out-of-order `seq` — per-edge serial `Send` is v0 (`zey`).
- Chain-length cap number (`7n8`, deferred) and remote-direct paths.
