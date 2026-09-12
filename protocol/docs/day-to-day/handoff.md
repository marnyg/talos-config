# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-12 — **M2 (`0bc.2`) designed via `/skill:grill-design`.** First
session with protocol-scope day-to-day files of its own.

- **ADR-0001** (Accepted, owner ruling same day) `technical/adrs/0001-one-chain-verifier-self-authenticating-envelopes.md`:
  one N-link `VerifyChain` folding `cert.Attenuate`; `Authorize` =
  ALPN→facet + that verifier on `[consent, grant]` + talos-only layer;
  aud ∈ {signer, `*`+postage, principal via aud-side speak-as};
  envelope/reply self-authenticating; one invocation = one bi-stream;
  proof-less replies; caveat vocab v2 (`endpoints`, `postage`).
- Glossary (`desired-state/domain-model.md`): new **Envelope,
  Invocation, Reply, Location record, Stream/actor facet**; sharpened
  **`seq`, Renewal beat, Facet** (root copy amended first — root is
  authoritative for Facet).
- `day-to-day/exploration-log.md` un-stubbed: nine ruled-out
  alternatives under §M2.
- Beads: `0bc.2.1` quint → `.2` cert → `.3` envelope → `.5` actor →
  `.6` iroh adapter → `.7` Talos-node deploy (deferred on `359.1.3`).
  `861` (hub as in-process actors) linked related — it is a consumer
  of the in-memory transport.

## Loose threads

- Quint-first ordering: `authorize.qnt` must state the N-link fold,
  aud-side speak-as (`cav.verbs ∋ invoke` now gates *presenting*, not
  only issuing), `*`-without-postage rejection, and intersection over
  `endpoints`/`postage` before `0bc.2.2` ports it.
- `protocol/doc.go`'s layout comment lists only `cert/` and `clock/`;
  update when `envelope/` and `actor/` land.
- Unchosen numbers: chain cap, `max_bytes`, mailbox depth, renewal
  fraction. Pick when a test forces it; record in the glossary.
- `group:` audiences, stranger replay, postage enforcement, lighthouse:
  deliberately outside M2 (M3).

## Suggested next steps

- Start `0bc.2.1` (Quint). Swarmable alone.
- ADR-0001 is Accepted; the exploration-log §M2 can be pruned once `.1` + `.2` land.
