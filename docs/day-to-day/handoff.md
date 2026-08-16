# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-16 (second session) — **Mesh policy phase 2 landed and
deployed; hub unsealed and verified** (sketch `6462fed4`).

- Committed the previous session's docs (they had been left
  uncommitted) + the sovereign-actor-protocol design doc.
- Phase 2 (`06014d6`): ephemeral in-memory policy overlay on
  `mesh.Manager` — `effectivePolicy()` now feeds all three render
  sites; wallet-gated `/policy` page beside `/status` (SIWE session
  views; set/clear are per-action signatures binding sha256(doc) +
  single-use nonce; git→overlay diff + export-to-commit text).
- Deployed twice (hub was still sealed between, so one unseal ceremony
  covered both); owner unsealed and verified. `/hosts` healthy from
  the laptop over the mesh: cp1 + tv online, w1 offline as expected.
  The "hub runs the pre-policy-refactor image" thread is closed.
- ADR-0014 actually written this session (last session's handoff
  claimed it existed; it did not) — Proposed, includes the invariant-2
  reading for ephemeral overlay state.

## Loose threads

- **ADR-0014 is Proposed** — owner flips to Accepted when happy; it
  also records the invariant-2 interpretation (ephemeral
  wallet-authorized state is not "server-owned state").
- **Parents' TV still not deployed** — all blockers cleared; recipe:
  `http://jellyfin.cp1.mesh.internal:30096`, local login.
- **Worker 6443 question** (untestable while w1 is down): check how
  workers join when w1 is power-cycled, before the storage arc.
- Phone enrolled as group `media`; re-enroll as `admins` if the :80
  SSO ingress is wanted from it.
- 90-day device renewal automation (`49443c38`) owed before
  ~2026-11-12.

## Suggested next steps

- Ship the APK to the parents' TV (the original build-gate).
- Phase 3 of `6462fed4`: `/policy` endpoint on the mesh HTTP surface +
  device hot-reload (`mobile.Tunnel` + nebup), building on the
  `effectivePolicy()` seam.
- Power-cycle w1 → answers the worker-6443 question, heals the
  degraded Longhorn volumes, unblocks the storage arc.
