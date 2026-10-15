# ADR-0010: Root rotation via a guardian-signed epoch chain; equivocation is capture

- Status: Proposed (2026-10-13, spike `talos-config-aja2`; model
  `verification/quint/rotation.qnt` and `protocol/cert/rotation.go`
  landed the same day — see FINDING under Consequences)
- Date: 2026-10-13 (spike `talos-config-aja2`, two review rounds)
- Builds: nothing yet (`protocol/cert` is the next wave). Adds: verb
  `rotate-root` to invariant 4's set (versioned). Amends: sketch
  § Identity ("Root compromised → … capture … no remedy", lines
  164–176), § "What each mechanism buys" row "Key compromise (roots,
  cold key)" (line 497), structural trade-off "Root capture has no
  in-protocol remedy" (`invariants.md:117`, replaced by the wording
  quoted below), glossary **Policy**, **Guardian**, **Epoch**,
  **Rotation**, **Equivocation**, **Pin authority**. Listed; edited
  by docs-update.

## Context and Problem Statement

The sketch makes root capture the one unrecoverable event: root keys
never expire, so a thief *is* the identity; the only defenses are
pre-compromise (cold storage; a contract account "outside the
protocol, invisible to it"). A wallet as a long-lived root is now
wanted (N=1: the owner's EOA), and a wallet is a hot key with a long
leash, not a cold one. Can a root be recoverable keeping one primitive
(inv 1), offline receiver-rooted verification (inv 2, verbatim) and a
chain-free core (inv 10, verbatim)? What a root thief does *not* hold
is the guardian keys — so the anchor is the
guardian set.

## Decision Drivers

- **One primitive**: rotation is certs in the existing shape; `Verify`
  stays "sig verifies under iss" (`protocol/cert/sign.go:81`).
- **Inv 2 and 10 verbatim**: a verifier advances on signatures it
  checks against something it already holds; never a chain read.
- **No second ordering mechanism**: no timelock, no veto, no
  time-as-authority (inv 9). Capture stays a declared threshold.
- **The on-chain account is not the identity** (inv 8).

## Considered Options

Anchor: a chain snapshot vs **the epoch-0 guardian set, pinned by
each verifier**. Ruled out: circular when current keys sign the
snapshot, a new RPC dependency class at leaves when verifiers read
it; breaks inv 10.

Fork handling: first-seen-wins vs **equivocation = capture (reject
both)**. Ruled out: first-seen-wins makes the first sighting a
safe-to-lose cache that is not safe to lose (ADR-0019's test).

Who rotates: root-signed removal with timelock + guardian veto vs
**only the guardian set, k-of-n; the root key never alone**. Ruled
out: a second ordering mechanism, time as authority, a fork generator.

Cert shape: multisig in `sig` / epoch in `iss` vs **k identical-body
certs, one per distinct guardian `iss`**. Ruled out: state in `iss`
and a second cert shape beside the primitive (inv 1).

Birth policy: parent-only guardian (`{parent}, k=1`) vs
**`{child-root, parent}, k=2`**. Ruled out: a parent that rotates the
child alone is the puppet the sketch already rejected.

## Decision Outcome

- **Identity = (address, epoch-0 policy `{root, guardians, k}`).**
  Two offline pins: a new actor's address derivation commits to the
  policy (a different policy is a different address); a legacy owner
  EOA is pinned at consent (talos: `talos/` as compiler input, like
  `allowed-signers`). A verifier advances its epoch only on a chain
  rooted at its own pin.
- **The guardian set is the identity's root.** The "root key" is a hot
  key with a longer leash; it never rotates anything alone. Capture
  threshold moves 1 → k-of-n; it does not vanish. `guardians0 =
  {root}, k=1` is today's security as a declared choice. Birth
  default `{child-root, parent}, k=2`: the parent recovers a stolen
  child root *with* the child, cannot capture alone; emancipation is
  a both-signed swap; a lost child root is leaf death (inv 7).
- **Primitive fit.** `rotate-root` joins the closed verb set (inv 4,
  versioned). A rotation is k certs, `iss` = the signing guardian,
  `aud` = the address rotated, `can = rotate-root`, `cav = {epoch,
  prev: H(policy_n), policy: policy_{n+1}}`, identical in `(aud, can,
  cav, iat, exp)`, one per distinct `iss ∈ policy_n.guardians`,
  `|certs| ≥ policy_n.k`; the verifier collects k. No multisig in
  `sig`, no state in `iss`. `exp` bounds **assembly** (k signatures
  gathered inside it), not validity: an expired rotation re-fed still
  proves the transition — signature-only provenance, as decision
  `7ry`. Inv 6 untouched: a rotation is a transition, not a presented
  credential.
- **Equivocation = capture.** Two *verifying* rotations with the same
  `(cav.epoch, cav.prev)` and different `policy`, **at a prev the
  verifier's own chain has reached** → the verifier rejects both; the
  identity is dead there until re-pinned out of band. The evidence is self-certifying (two
  bodies, k signatures each) and travels on the beat like the
  blocklist; it is **not** safe-to-lose by ADR-0019's test (forgetting
  it yields a stronger grant), so git holds the durable copy.
- **The on-chain account is a follower.** It verifies the same
  signatures, lags, never diverges — except that transaction order
  resolves the one fork the protocol declares dead; money only.
  Forces: guardians are secp256k1 keys (no Ed25519 precompile);
  rotation bodies are typed so wallet-signed bytes == contract-
  verified bytes. Certs stay pure "may" (inv 8).
- **N=1 naming.** The ssh signing key (root ADR-0030,
  `allowed-signers`) is a **pin authority** for the hub's view (via
  `#bundle`, the fleet's), not a guardian: a guardian rotates the
  identity for everyone; a pin authority re-pins one verifier's view.
  Wallet policy = identity; ssh/age key = pin authority + secrecy.

### Consequences

- Good: capture is bounded by a policy the identity chose; inv 2 and
  10 verbatim; no new cert shape; the fork is answered once.
- **FINDING (model-first, `rotation.qnt`, `aja2.1`)**: as first
  worded ("same `prev`", no rooting) equivocation was a free
  kill-switch — anyone invents a policy P' with their own keys as
  guardians and equivocates from it. Death needs a *rooted* pair; the
  key is `(epoch, prev)` because `H(policy)` recurs. Pins 1, 3, 4 were
  not refuted. The quoted wording below carries the amendment.
- Residual — **truncated history**: a verifier that first meets the
  identity after capture and is shown a truncated chain is as exposed
  as any starved verifier; a chain read closes it by choice only.
- Residual — **lost equivocation record**: a verifier that loses its
  copy before the proof spreads may accept the thief's branch; the
  durable copy is git for talos, unnamed for other consumers.
- Open: inv 2's "first link is signed by the receiver" — a rotation
  chain's first link is guardian-signed, rooted at the receiver's
  *pin* (its own consent commits to `policy_0`); whether that keeps
  inv 2 verbatim or needs a sentence is not settled here.
- Open: inv 3 vs the birth default — a child cannot change its
  guardian set without the parent until emancipated (a shared
  threshold, not an authority above); sketch § Open problems item 3
  ("parent compromise is worse") gains "the parent holds half of
  every child's recovery".
- Open: `exp` not gating validity is a first for a cert class;
  `Authorize` must branch on `can = rotate-root` to skip the expiry
  judgment — pinned in the `protocol/cert` wave against the model.

Trust: a thief holding the root key *and* k guardian keys is the
identity — today's statement with the threshold moved.

> Root capture is bounded by the identity's own policy. An identity
> is an address plus an epoch-0 policy {root, guardians, k} it commits
> to (by address derivation, or by pin at consent). The guardian set
> is the identity's root; the root key is a hot key with a longer
> leash, and capture threshold is k-of-n. Rotation is k
> guardian-signed rotate-root certs with identical bodies, verified
> offline; a verifier advances its epoch only on a chain rooted at its
> own pin, and exp on a rotation bounds assembly, not validity. Two
> verifying rotations from the same predecessor, at a predecessor the
> verifier's own chain has reached, are equivocation; a verifier
> rejects both — the identity is dead there until re-pinned
> out of band, and the proof travels. An identity with no guardians
> chose irrecoverability. A verifier that first meets an identity
> after capture and is shown a truncated history is as exposed as any
> starved verifier — it may close that window with a chain read, by
> choice, never by obligation. The on-chain account follows the cert
> chain and may lag, never diverge, except that chain ordering
> resolves an equivocation the protocol declares dead — money only.

(Spike `talos-config-aja2`; no decision id until accepted.)
