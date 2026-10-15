# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-13: **root rotation designed and built (spike `talos-config-aja2`,
protocol ADR-0010 Proposed)** — a review session on the repo's ideas
turned into a design: keep "the wallet is the identity", make the root
recoverable without touching protocol inv 2/10. Swarm of three
(`538330a` ADR, `038461e` `rotation.qnt`, `1eaebf6` `cert/rotation.go`),
all gates re-run (`check.sh run`, verify depth 7, `go test`,
`scripts/test-iroh.sh` green). Details in
`protocol/docs/day-to-day/handoff.md`.

- The N=1 consequence to name: the owner has **two roots with distinct
  jobs** — wallet policy = identity (guardians rotate it for everyone);
  the ssh/age key (ADR-0030 `allowed-signers`) = **pin authority** for
  the hub's view of that identity + confidentiality root. Nothing in
  `talos/` changes until the wallet declares guardians; today's
  `guardians0 = {root}, k=1` is the declared choice.
- `4mg` gained a note: EIP-712 typed signing + decoupling the secrets
  seed from a wallet signature are one decision superseding `ce8`.

## Loose threads

- **Desired-state amendments awaiting the owner** (surfaced by
  docs-update, not applied): protocol inv 4 verb set (+`rotate-root`),
  the "root capture" structural trade-off → ADR-0010's wording (with
  the model's "rooted" amendment), inv 2 / inv 6 wording for a cert
  class that is a transition; root domain-model glossary **Pin
  authority**; protocol glossary terms.
- `aja2.1/.2/.3` merged, **awaiting owner close**; `aja2` closes once
  the amendments are ruled. `e6pu.2.{1,2,3}` still awaiting close from
  2026-10-12.
- vendorHash for config-server-bin / iroh-transport / actors-bin is
  stale after the `protocol/cert` change — bumped in this session's
  landing commit if nix finished; otherwise the pre-push hook says so.
- Threads to file (from worker reports): birth receipt that keeps
  inv 13 (`{lease, image}` only; child binds its key to the lease);
  `Policy.Hash` vs EIP-712 struct hash; rotate-root certs carrying
  base caveats; the sketch's "negotiated guardians" cell overloading
  *guardian*.
- Standing: 9 `<!-- stale? -->` flags; `jlgz`, `bsj` (blocks `ivof`),
  `bhui`; the `e6pu` live acceptance (sandbox-agent image + spawn).

## Suggested next steps

- Rule the amendments above; `bd close` the three tasks; ADR-0010 →
  Accepted when the hub consumes `Pin`.
- Back to `ivof`: rebuild `sandbox-agent` with the child and run the
  live acceptance, then the parent actor.
- `4mg` as one decision (EIP-712 + seed), now that `ce8`'s objection
  is gone.
