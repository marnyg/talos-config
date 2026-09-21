# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-23 — **M3's loose threads closed** (ADR-0007 consequences
updated in place; decisions `gfyj`, `rpoe`; `jjti` closed).

- **Numbers chosen** (`4e6a63c`): `postage.DefaultPoWBits = 22`
  (`DefaultRequire`; measured ~15 MH/s ⇒ ~0.25 s laptop, ~1 s phone,
  ≈ 5000× the receiver's refusal cost); frontdoor tail is *no*
  constant — `Frontdoor` refuses `ttl ≤ 0`, guidance is the location
  record's ttl; `lighthouse.DefaultMaxRecords = 4096`,
  **refuse-when-full, never evict** (`ErrDirectoryFull`; expired
  entries swept first; a listed member always re-publishes).
- **Quint** (`5506984`): `authorize.qnt` already covered the postage
  caveat + `publish` verb; the header now records that `Scheme.Check`
  and the inbox order are deliberately unmodelled (Go tests pin them).
- **`jjti`** (`597c13c`): `Actor.refusesUnstamped` in the transport
  goroutine — an unstamped envelope to a facet whose *every* candidate
  root consent demands postage is answered `postage` before the
  mailbox. Refusal-only; owner accepted the principle (a check outside
  the fold is fine when it can only refuse). Pinned by
  `TestUnstampedIsRefusedBeforeTheMailbox` (sender's seq mark stays 0).
- **Found + fixed a real M3 gap**: `cert.VerifyChain` returned the
  *first* accepting verdict in `Consents` order, so a member named by a
  free consent could be charged postage if the frontdoor was held
  first. Now prefers a non-group, postage-free verdict
  (`TestVerifyChainPrefersPostageFreeVerdict`, both orders).
- `#lookup` must name ids (`ErrNoIDs`, `587175d`): the wire never
  enumerates the directory; `Records()` with no ids stays owner-side.
- vendorHashes bumped (`f3d57a7`) and verified with `--rebuild`;
  config-server and iroh-transport build unchanged.

## Loose threads

- `fh2y` (thread, P3): every pre-mailbox refusal (bad-sig, wrong
  target, unstamped) still signs a reply — a flood costs one ed25519
  sign per envelope, ≈ the verify it saved. "Errors are replies"
  (ADR-0001) vs drop-without-reply is undecided.
- Whether "refusal-only checks outside the fold are admissible" wants
  its own ADR or stays an ADR-0007 amendment — owner to say.
- No spent-token set (open problem 8) — by design, stated.
- Carried: ADR-0001's ≈ 1 h `reach-me-at` text; held `speak-as` out of
  `Result.Verified` (ADR-0003); `DefaultMailbox = 64`; open problem 9
  still open for delegation windows / renewal beat / tranche sizes.

## Suggested next steps

- **M4 `0bc.4`** (spawn-as-k8s-Job) — unblocked; start with a
  grill-design pass on the spawn/intro handshake and the provider seam.
- Talos consumer: replace the Phase-1 "lighthouse as a view" (root
  ADR-0024) with `protocol/lighthouse` when a second network exists.
