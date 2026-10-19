# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-10-23 — **the durable pin copy (`d0o0`, talos consumer side)**:
`talos/pins/<scheme>-<address>.json` `{address, policy0, rotations}`
read by `config-server/policy.LoadPins` at hub construction and on
every served git tip; `FoldRotations` is now one function for hub and
node; a reload that un-admits the held speak-as issuer re-seals the
hub (`issuer.Seal`). No protocol change; ADR-0011's consumer-wiring
paragraph names the file. Closes the "persistence" thread below once
deployed; ADR-0010/0011 can go Accepted after that.

Before that, 2026-10-18 — `djdl` (swarm worker `q-pin-laws`, `49e7f62`):
`authorize.qnt`'s `res`/`chainRes` are the PINNED receiver; the five
"consented sovereign vouches for key" laws carry a raw `pinnedAs(w, k)`
edge (`consentedKey(k)` for speak-as issuers); three negative bridge
laws dropped as redundant, `invTrivialPinsPreserveVerdicts` keeps an
explicit `resNoPins`, `invNewRootAdmittedViaPin` stays. 12-mutant
table all killed. `resolve` lost its no-pin ITE branch (same
semantics): depth-2 `invAll` 409 → 333 s wall — still over the 300 s
budget (nightly tier). ADR-0011's "pre-d0o0 laws quantify the unpinned
receiver" Open bullet is closed; rule 3 aud-binding (`l3on`) is not.

Before that, 2026-10-17 — `7w5` (swarm worker `f-actor-silent`, `744cc9f`):
`Actor.serve` closes the stream unanswered on a failed envelope
signature, like undecodable bytes; wrong-target/postage rejects still
sign. The test asserts no Signer call. Domain-model Mailbox entry
updated. Open: `rejectStatus` still maps `envelope.ErrSig → bad-sig`
from the loop (really a chain-signature failure → `unauthorized`?);
`fh2y` (drop the other pre-mailbox rejects too) unchanged.

Before that, 2026-10-15 — **receiver-held pins wired into authorization (`d0o0`,
ADR-0011 Proposed)**, swarm of three (`c5705eb` ADR, `0870a68`
`authorize.qnt`, `3388cac` code), gates re-run by the orchestrator.

- **Rule**: `Receiver{ID, Consents, SpeakAs, Pins}`; issuer resolution
  gains a hop that is **subtractive for pinned addresses** —
  `(ks \ pinned) ∪ {p.Address | ∃k ∈ ks: p.Admits(k)}`. A pinned
  identity is reachable only through its pin; a trivial pin changes
  no verdict. **FINDING** (`d-pin-adr` → `authorize.qnt` mutant p1):
  the additive first cut let the old EOA key keep resolving to itself
  (identity address == epoch-0 key) and match the consent.
- **Model**: five pin laws, mutation-tested; `res` stayed the unpinned
  receiver and `resPin` the pinned one, with bridge laws — superseded
  2026-10-18 (`djdl`, above: `res` is pinned).
  Depth-2 verify of `invAll` is now ~305–342 s (over the 300 s budget;
  accepted — nightly tier; the regression is in pre-existing chain
  laws from the extra `OWNER1_NEW` draw). `invTrivialPinsPreserveVerdicts`
  has its own verify line.
- **Code**: `cert.resolve` ported 1:1; `sovereign.ViaPin`;
  `envelope.Receiver.Pins`; `actor.HoldPins`/`Pins()`; hub pins per
  admin address + `unsealIssuer` via admitted roots; member pin per
  Kit identity, `#bundle.rotations` folded copy-on-write; rule 3 (aud
  binding) deliberately not pin-aware.

## Loose threads

- Owner close: `d0o0.1/.2/.3` merged; `d0o0` itself closes when the
  durable copy (git) of pins/rotations exists — or re-scope it.
- Open from ADR-0011: rule 3 not pin-aware (a root presenting a
  consent-only chain itself); pre-d0o0 laws quantify the unpinned
  receiver (follow-up: restate with a raw pin edge, make `res` pinned).
- `unsealIssuer` returns the admitting KEY's address, not the identity;
  the ce8 master-wallet pairing reads it — check when a real rotation
  exists.
- Persistence: hub pins come from `talos/pins/` (2026-10-23); member
  pins still survive only via the persisted bundle's `rotations` —
  fine while the hub's copy is durable, since every beat re-feeds it.
- Still open: `kckm`, `lm2a`, `fh2y`; chain-on-connect ADR question;
  `whxz`, `e7ru`.

## Suggested next steps

- Deploy the hub on the pins build; then ADR-0010/0011 → Accepted.
- Restate the five "consented sovereign vouches for key" laws
  pin-aware (`authorize.qnt`), re-measure verify.
- EIP-712 + seed decoupling as one decision superseding `ce8` (`4mg`).
