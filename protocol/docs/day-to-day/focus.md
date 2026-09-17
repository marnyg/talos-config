# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **The verifier is complete for Phase 1; the talos hub becomes
the protocol's first real consumer.** `cert`, `clock`, `envelope`,
`actor` are pinned to `authorize.qnt`/`clock.qnt` (model leads, Go
follows 1:1). `xwu` (ADR-0002, verb = root consent's), `kau` (ADR-0003,
a receiver answers for principals whose `speak-as` it holds —
`cert.Receiver`) and `7ei` (`#renew` binds `aud` like rule 3) all
landed 2026-09-17/18. The hub is consuming: its Issuer and Enroll are
live `Actor`s over `MemoryNetwork` (`actor.Hold` landed for the unseal
lifecycle, 2026-09-17). The next protocol-side change is
**ADR-0004 (`zeb`)**: `cav.target` admits the wildcard `"*"` for the
policy compiler (`359.8.5`) — model first, then `cert`. M3 (`0bc.3`: lighthouse as a plain
actor, PoW postage for strangers) is unblocked on the protocol side —
its `#publish` facet binds verb `publish` through the same fold; in the
talos deployment the Phase 1 lighthouse is a view over the Issuer's
location cache (root ADR-0024).

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
