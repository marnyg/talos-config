# Current Focus — sovereign-actor protocol

<!-- Forward-looking for the protocol scope. ~20 lines. -->

**Now:** **M3 is built and its threads are closed (2026-09-23):** the
lighthouse is a plain actor, strangers pay `pow:22` postage at the
frontdoor, unstamped traffic to a postage-only facet is refused before
the mailbox, the directory is capped (4096, refuse-when-full), and the
verifier prefers a postage-free verdict for a caller some consent
names. `Actor.Verbs` binds `#publish → publish` through the existing
fold; the envelope has one optional signed key (`postage`). **Next on
the roadmap is M4 (`0bc.4`, spawn as funded enrollment)** — unblocked.
Below is the M2 state this builds on.

**Before:** **The verifier is complete for Phase 1; the talos hub becomes
the protocol's first real consumer.** `cert`, `clock`, `envelope`,
`actor` are pinned to `authorize.qnt`/`clock.qnt` (model leads, Go
follows 1:1). `xwu` (ADR-0002, verb = root consent's), `kau` (ADR-0003,
a receiver answers for principals whose `speak-as` it holds —
`cert.Receiver`) and `7ei` (`#renew` binds `aud` like rule 3) all
landed 2026-09-17/18. The hub is consuming: its Issuer and Enroll are
live `Actor`s over `MemoryNetwork` (`actor.Hold` landed for the unseal
lifecycle, 2026-09-17). **ADR-0004 (`zeb`) landed 2026-09-18**:
`cav.target` admits the wildcard `"*"`, and the talos policy compiler
(`config-server/policy`) is its first consumer. No protocol change is
queued by the consumer's next steps. M3 (`0bc.3`: lighthouse as a plain
actor, PoW postage for strangers) is unblocked on the protocol side —
its `#publish` facet binds verb `publish` through the same fold; in the
talos deployment the Phase 1 lighthouse is a view over the Issuer's
location cache (root ADR-0024). **2026-09-19: the first receiver
outside the hub is live** — the talos node agent runs `cert.Authorize`
on stream-facet connections and beats the hub; it asked for
`Actor.SeqBase` and `Actor.Observe`, the third consumer-driven runtime
addition (`t29`). **2026-09-19 (later): the first consumer-driven
regression** — `SeqBase` seeded from `UnixNano` broke exactness under
JCS; `seq` is now an I-JSON integer refused out of range on both sides
(ADR-0006, Accepted).

**Toward goal:** `desired-state/goals.md` — *One primitive* (one
verifier for every verb), *Offline, receiver-rooted authorization*,
*Deployment-independent transport* (`Transport` interface; iroh is an
adapter module that `protocol/` never imports).

**Out of scope:**
- M4–M5: spawn, money. (M3 lighthouse + postage landed 2026-09-22;
  threads closed 2026-09-23.)
- Real nodes: `0bc.2.7` is deferred on the Talos extension probe `359.1.3`.
- Persistence, supervision, store-and-forward (invariant 12).
- Windowed/out-of-order `seq` — per-edge serial `Send` is v0 (`zey`);
  a persisted per-receiver counter (clock seeding suffices).
- Chain-length cap number (`7n8`, deferred) and remote-direct paths.
