# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-10-13 — **root rotation via a guardian-signed epoch chain (spike
`talos-config-aja2`, ADR-0010 Proposed)**, built by a three-worker
swarm (`538330a` ADR, `038461e` model, `1eaebf6` code), gates re-run
by the orchestrator.

- **Design** (two review rounds, pinned as notes on `aja2`): an
  identity is `(address, epoch-0 policy {root, guardians, k})`; the
  guardian set is the identity's root, the root key a hot key with a
  longer leash; rotation is k `rotate-root` certs identical in
  `(aud, can, cav, iat, exp)`, `iss` = each signing guardian; a
  verifier advances only on a chain folded from its own pin;
  equivocation = capture. Inv 2 and 10 stay verbatim — no chain read
  anywhere in identity; the on-chain account is a follower.
- **`verification/quint/rotation.qnt`** (`aja2.1`): nine laws,
  mutation-tested; verify depth 7 (~115 s). **FINDING**: equivocation
  must be *rooted* (at an `(epoch, prev)` the verifier's chain has
  reached) or an invented policy is a free kill-switch. Two residuals
  checked negatively in `check.sh`: truncated history (pin 1) and a
  lost equivocation record (pin 2).
- **`protocol/cert/rotation.go`** (`aja2.3`): `Policy.Hash` (keccak
  over JCS), `AssembleRotation` (exp read only here), `Pin{Hold, View,
  Admits, Held}` — the view is a fold, never stored; unchained
  rotations are kept. Caveat vocabulary **v4** (`epoch`, `prev`,
  `policy`) is serialised only on rotate-root certs, so every other
  verb's signed bytes are unchanged. `Authorize`/`VerifyChain`/
  `Attenuate` reject rotate-root fail-closed; it never feeds the mark.

## Loose threads

- Desired-state not yet amended (owner to confirm): inv 4's verb set,
  the "root capture" structural trade-off, inv 2 / inv 6 wording for a
  cert class that is a transition, glossary terms (Policy, Guardian,
  Epoch, Rotation, Equivocation, Pin, View, Pin authority).
- `Pin.Admits` is not wired anywhere; the hub judging a `speak-as`
  issuer against a pinned view is the first consumer (`kckm`
  territory). The equivocation proof has no transport yet (`#bundle`
  like the blocklist, git as the durable copy).
- `Policy.Hash` will collide with EIP-712's struct hash when pin 5
  lands; treat `prev` as opaque until then (`4mg`).
- Open from the code report: should rotate-root certs be required to
  carry empty base caveats; `Hold` returning `ErrNotRooted` while
  keeping the rotation; rootCerts skipping rotation certs vs inv 9.
- Still open from before: `kckm`, `lm2a`, `fh2y`; chain-on-connect ADR
  question (2026-10-12).

## Suggested next steps

- Owner rulings on the desired-state amendments above, then
  ADR-0010 → Accepted.
- Wire `Pin` into the hub's speak-as resolution behind a pinned
  policy for the N=1 wallet (`guardians0 = {root}, k = 1` first).
- EIP-712 + seed decoupling as one decision superseding `ce8` (`4mg`).
