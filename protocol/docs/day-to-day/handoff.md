# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-10-15 — **receiver-held pins wired into authorization (`d0o0`,
ADR-0011 Proposed)**, swarm of three (`c5705eb` ADR, `0870a68`
`authorize.qnt`, `3388cac` code), gates re-run by the orchestrator.

- **Rule**: `Receiver{ID, Consents, SpeakAs, Pins}`; issuer resolution
  gains a hop that is **subtractive for pinned addresses** —
  `(ks \ pinned) ∪ {p.Address | ∃k ∈ ks: p.Admits(k)}`. A pinned
  identity is reachable only through its pin; a trivial pin changes
  no verdict. **FINDING** (`d-pin-adr` → `authorize.qnt` mutant p1):
  the additive first cut let the old EOA key keep resolving to itself
  (identity address == epoch-0 key) and match the consent.
- **Model**: five pin laws, mutation-tested; `res` stays the unpinned
  receiver and `resPin` the pinned one, with bridge laws (trivial ⇒
  equal; rotated ⇒ equals unpinned with re-addressed consents).
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
- Persistence: hub pins are memory-only; member pins survive only via
  the persisted bundle's `rotations`. ADR-0010 says the equivocation
  record is not safe-to-lose → `talos/` must carry `{policy0,
  rotations}` per pinned address (format sketched in the code report).
- Still open: `kckm`, `lm2a`, `fh2y`; chain-on-connect ADR question;
  `whxz`, `e7ru`.

## Suggested next steps

- `talos/pins/<address>.json` (or a block in mesh-policy) → hub
  `Pin.Hold` at construction; then ADR-0010/0011 → Accepted.
- Restate the five "consented sovereign vouches for key" laws
  pin-aware (`authorize.qnt`), re-measure verify.
- EIP-712 + seed decoupling as one decision superseding `ce8` (`4mg`).
