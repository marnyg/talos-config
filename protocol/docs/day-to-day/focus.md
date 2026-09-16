# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **Two small verifier rulings to land, model first, before the
talos hub becomes the protocol's first real consumer.** `cert`,
`clock`, `envelope`, `actor` are pinned to `authorize.qnt`/`clock.qnt`
(model leads, Go follows 1:1). Pending in `VerifyChain`: **`xwu`** (verb
= root consent's verb, protocol ADR-0002) and **`kau`** (a receiver
answers for a principal it holds a live `speak-as` from, protocol
ADR-0003 — so grants can name a cold root served by a rotating hot
key); then **`7ei`** in `#renew`. These gate talos `359.8.1`
(membership issuance). M3 (`0bc.3`: lighthouse as a plain actor, PoW
postage for strangers) is unblocked by the Phase 0 gate (2026-09-16)
and waits only on `xwu`; in the talos deployment the Phase 1 lighthouse
is a view over the Issuer's location cache (root ADR-0024).

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
