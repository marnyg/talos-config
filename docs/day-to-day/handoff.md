# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-12 — **M2 designed: `/skill:grill-design` on `0bc.2`**
(`main bf31649 → HEAD`). Docs-only session; no code. Six branches
walked, one ADR, six worker-sized beads. Protocol-scope detail is in
`protocol/docs/day-to-day/handoff.md`.

- **Protocol ADR-0001** (Proposed): one N-link chain verifier folding
  `cert.Attenuate`; today's `Authorize` becomes its connection-level
  special case. Envelopes are self-authenticating (transport peer is a
  hint, never authority ⇒ `seq` HWM is load-bearing). Facets split:
  **actor facet** (`to.facet` in the encrypted envelope, primary) vs
  **stream facet** (ALPN at connect, the *compatibility mode* that lets
  a node be a plain VPN in front of Jellyfin). Aud-side speak-as
  (`cav.verbs ∋ invoke`). Caveat vocab v2: `endpoints`, `postage`.
- Root glossary **Facet** amended (authoritative copy; protocol copy
  matches). `docs/desired-state/domain-model.md:306`.
- `0bc.2` retitled/described; children `.1 quint → .2 cert → .3
  envelope → .5 actor → .6 iroh adapter → .7 Talos-node deploy
  (deferred on `359.1.3`)`. Nebula ruled out as M2 transport; "on
  cp1/w1" is `.7`, not M2's gate. Handover `2ud` closed.

## Loose threads

- **Ordering cost accepted:** `authorize.qnt` must gain the N-link
  chain, aud-side speak-as and the two caveats **before** the Go port
  (`0bc.2.1` blocks everything). Speak-as `cav.verbs` currently gates
  *issuing*; using it for *presenting* is a semantic extension the
  model must state.
- Numbers still unchosen (open problem 9): chain-length cap,
  `max_bytes`, mailbox depth, renewal fraction of shortest held
  lifetime.
- `group:` audiences stay connection-level (talos layer) — no member
  cert in the envelope path. Revisit only when an actor-facet use
  appears.
- Stranger (`aud:*`) replay is M3's postage problem; the `seq` HWM
  table must be keyed for known correspondents only.
- Carried: `49x` yamlfmt quirk; `359.8.5` `6z9` questions; `pkgsStatic`
  Talos link unattempted (now a line item in `0bc.2.6`); `54n`
  boot-token HMAC; ADR-0017/0019 still Proposed; GH cache 7-day
  eviction.

## Suggested next steps

- `0bc.2.1` — extend `verification/quint/authorize.qnt` per ADR-0001
  (swarmable alone; `0bc.2.2` follows it).
- Promote ADR-0001 to Accepted once `.1` + `.2` land.
- Phase 0 probes `359.1.1–.3` when fly scratch + Android exist.
