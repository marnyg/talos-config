# ADR-0008: Birth is a facet behind a per-spawn open consent; the nonce is correlation

- Status: Proposed
- Date: 2026-09-21 (grill-design, `talos-config-0bc.4`)
- Builds: M4 (`protocol/spawn`). Amends: sketch § Spawning (the birth
  message is an envelope, not `{child_pubkey, sign_C(nonce)}` to raw
  endpoints), glossary **Intro**, **`#birth` facet**, **Birth
  window**, **Starter kit**, **Intro nonce**.

## Context and Problem Statement

The sketch describes birth as the newborn sending
`{child_pubkey, sign_C(nonce)}` "to the intro endpoints". But every
message in this system is a self-authenticating envelope whose chain
must root in a consent the *receiver* signed (invariant 2,
`actor.process` → `VerifyChain`). At birth the child holds no cert from
its parent, so the literal sketch cannot pass the parent's verifier.
Three questions: what the birth message is on the wire; how the
parent's location reaches a child that has no lighthouse access; and
what the parent must hand back for the child to survive.

## Decision Drivers

- **One primitive** (invariant 1): no second authority mechanism, no
  new verifier path for a one-shot message.
- **The nonce stays the only bearer token** (sketch), and it should
  grant as little as possible.
- **Revocation is expiry** (invariant 6): the birth window should be a
  cert lifetime, not separate nonce bookkeeping.
- **Raw addresses only in bootstrap artifacts** (invariant 11), and
  preferably not even there when a signed record is as cheap.
- **Private keys never travel** (invariant 7); a restarted container
  is a new identity.

## Considered Options

Birth message:

1. Multiplex on `#frontdoor` with `{kind: birth, nonce}` in the
   payload.
2. The intro carries a P-signed cert `{aud: "*", facet: #birth,
   cav.nonce}` replacing the raw nonce.
3. **A dedicated `#birth` facet behind an aud-`*` consent of the
   frontdoor's shape (postage required), nonce in the payload.**

Birth consent lifetime: per-parent (renewed on the beat) vs
**per-spawn** (`exp` = birth window).

Nonce reuse inside the window: last-come-wins vs **first key binds**.

Where P is, in the intro: raw endpoints vs **P's signed reach-me-at
record** vs lighthouse rendezvous (deferred, additive).

Starter kit minimum: **one chain `P→C invoke {target: P, facet:
#renew}`** vs a richer mandated set (`#report`, a parent-facing facet
on the child, a funding slot).

## Decision Outcome

Option 3, per-spawn, first-key-binds, signed reach-me-at, one mandated
chain.

- **Intro** = `{parent, location, consent, nonce}`: P's id, P's
  current reach-me-at cert, the per-spawn birth consent
  `{iss: P, aud: "*", can: invoke, cav: {target: [P], facet: [#birth],
  postage}, exp: now + birth window}`, the nonce. Two P-signed certs,
  one JSON blob, opaque to the provisioner.
- **Birth message** = an ordinary envelope from C to `P#birth`,
  payload `{nonce}`, stamped (the child pays one PoW to its parent;
  `refusesUnstamped` guards the facet for free). The envelope's
  signature binds C's key to the nonce; the piggybacked reach-me-at
  is C's first location record. `inv.From` is the child's id.
- **Pending-spawn table**: nonce → spawn record, volatile. The nonce
  binds to the first `From` that presents it; the same key re-knocking
  is idempotent (kit re-issued), any other key is refused. Entries are
  swept at the consent's `exp` — the birth window is one number.
- **Starter kit** = `{grants: [[cert…]…], locations: [reach-me-at…]}`
  in the reply body; the protocol mandates exactly the `#renew` chain.
  The child installs each chain under every `(target, facet)` its last
  link names. P→C authority never crosses the wire: the child mints
  and holds `{aud: P, target: C, facet: [app facets]}` itself.
- **Not mandated**: a parent-facing facet on the child (stopping is
  the lease's job), a funding field (M5), key persistence (a newborn
  whose key does not survive a restart is dead; k8s runs the Job with
  `restartPolicy: Never`, `backoffLimit: 0`).

### Consequences

- Good: no new verifier path; birth is `Send` + a handler. The birth
  window, the consent tail and the location ttl are minted together.
  A leaked intro buys one knock window against a postage-guarded
  facet and nothing else.
- Bad: a parent that moves inside the birth window loses that birth
  (GC by lapse). Upgrade is additive — a lighthouse lookup-cap in the
  intro (thread).
- Bad: a parent restart inside the window loses the pending table;
  the child's retries get `unknown nonce` until the lease lapses.
  Same GC, stated.
- Trust unchanged: the provisioner sees the intro and can complete the
  handshake as a fake child (sketch open problem 2).
