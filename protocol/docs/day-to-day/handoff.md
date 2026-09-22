# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-22 — **`ydq0`: M3 broke the node beat; fixed receiver-side.**
Not planned work — a talos deploy surfaced it (the hub image runs this
suite), and `main` had been undeployable since `5c2bf7d`.

- **Symptom**: `TestHubBeatOverIroh` and `TestNodeAgentEndToEnd` failed
  with `ErrAudUnbound` on `#bundle`. A hub built from `main` would have
  refused every node's renewal beat — the whole fleet, silently, on
  the next deploy.
- **Cause**: M3 gave `Send` a caller-side rule — drop a held chain's
  first link when the receiver signed it — for the frontdoor case (a
  stranger presents an empty chain; the receiver roots with its own
  consent). But *receiver-signed* does not imply *the receiver's own
  consent*: the talos hub's beat grant is signed by its hot key **as
  the wallet** (ADR-0018), so it is a link, and it was being stripped.
  The caller cannot tell the two shapes apart — only the receiver
  knows what it holds as consents.
- **Fix**: the rule moved into the verifier. `chainUnder` folds a chain
  whose first link is byte-equal (by `sig`) to the consent it is
  folding under *without* that link — per consent, so a frontdoor the
  receiver has since dropped roots nothing (the grant is the record).
  `proofFor` now presents the held chain unchanged. Law:
  `TestVerifyChainOwnConsentPresented`. ADR-0007 carries a revision
  note; domain-model updated.
- Verified on the real hub afterwards: a fresh node enrolled and
  logged `beat ok`.

## Previous session

2026-09-24 — **M4 designed, not built** (grill-design on `0bc.4`;
docs only, no Go touched, no vendorHash moved).

- **ADR-0008 (Proposed)** — birth is an ordinary envelope to a
  dedicated `#birth` facet behind a **per-spawn** aud-`*` consent of
  the frontdoor's shape (postage required; the child pays one PoW to
  its parent); the nonce is correlation, **binds to the first key**
  (same-key re-knock idempotent, others refused); the intro is
  `{parent, location: P's signed reach-me-at, consent, nonce}`; the
  starter kit `{grants, locations}` mandates exactly the `#renew`
  chain; P→C authority is the child's own consent, never on the wire.
- **ADR-0009 (Proposed) + invariant 13** — the **provisioner is an
  actor** (`#spawn`/`#extend`/`#kill`) that knows leases and never
  learns about birth; the Go `Driver{Start, Extend, Kill}` is *its*
  per-platform seam, outside `protocol/`; leases are **passive**
  (deadline = the renewed cert's `exp`, sent by a decorator on the
  installed `#renew` handler via `AcceptTable`); a child
  **self-lapses** on "no live edge". Spawner/Provisioner/Driver/Lease/
  Self-lapse/Intro/Starter kit are glossary terms.
- **Acceptance**: fake driver in protocol tests **plus k8s Job and
  docker drivers** behind one provisioner binary — owner wants two
  substrates so the child image is forced self-contained.
- Cross-scope: the talos **Provisioner** is a Phase-1 fused
  spawner+provisioner for bare metal (boot token = intro nonce,
  `/enroll/machine` = `#birth`, Kit = starter kit); noted in the root
  glossary, split filed as thread `kckm`.
- Beads: decisions `1a0q`, `9fuo`, `3f41` (closed); sub-tasks
  `0bc.4.1–.6`; threads `lm2a` (lighthouse lookup-cap in the intro),
  `kckm`.

## Loose threads

- **`verification/quint/authorize.qnt` does not carry the own-consent
  strip** (`chainUnder`, line ~420). The standing rule is *change the
  model before the Go*; `ydq0` went the other way under deploy
  pressure, so model and implementation disagree until it is ported.
  The 18 chain laws still pass — the strip is a new law, not a changed
  one.
- `Job.spec.activeDeadlineSeconds` mutability on a live Job is
  asserted, not verified — check in `0bc.4.3`; fallback is the
  docker-style label sweep.
- The provisioner's consent to its customers is v0 = the parent's key;
  frontdoor/negotiated offer (the market) is M5-adjacent, unmodelled.
- `#extend` is I/O inside a serial-mailbox handler — synchronous in v0.
- The `payment` argument of the sketch's `spawn` is absent in v0 (M5).
- Carried from M3: `fh2y` (refusals still sign a reply); whether
  "refusal-only checks outside the fold" wants its own ADR; open
  problems 8 and 9.

## Suggested next steps

- Build `0bc.4.1` (`protocol/spawn` over `MemoryNetwork` with a fake
  driver) — the test is the spec for ADR-0008.
- Then `0bc.4.2` (provisioner wire + `#renew` decorator); drivers and
  binaries after, in a module outside `protocol/`.
- Promote ADR-0008/0009 to Accepted at `0bc.4.6`; then prune the
  exploration-log §M4 like §M2 was.
