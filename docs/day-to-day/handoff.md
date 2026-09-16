# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-16 (late) — **Phase 1 groomed, then the hub actor cut designed
(`359.8.2.1`, grill-design). No code changed.**

- **Grooming**: `359.8` is a DAG, not a line (`mesh-v3-iroh.md
  §Phase 1`). `359.8.2` split into `.2.1` design / `.2.2` relay in hub /
  `.2.3` Enroll + `#bundle`; protocol `xwu`, `7ei` and new `kau` are
  hard deps of `359.8.1`; the stale `359.8.1 → 359.8.2` edge was
  removed (it blocked the parent through its own child). P0.2 Android
  findings filed as `359.9.4.1–3`. Duplicates `dj5`/`rjg` closed.
- **Design** (root **ADR-0024**, protocol **ADR-0003**, both Proposed;
  domain-model §2 "Hub actors" table + diagram): one key per hub actor,
  only the Issuer's is `speak-as.aud` (`hubkey`) and the hub's iroh
  identity; Enroll/Provisioner keys have no wallet delegation, the
  Issuer consents to them at boot and verifies the wallet's proof
  inside their requests. **Grants to hub facets name the wallet**, or
  the first beat after every deploy deadlocks — `VerifyChain` rule 4
  grows "or a principal the receiver holds a live `speak-as` from"
  (`kau`, model first). Envelope target and dial id stay `hubkey`.
  **Beat is actor-native**: `#renew` + `#bundle` (decision `mdv`
  revises `itb`; `hub-http` keeps only `/config`). Lighthouse in P1 =
  view over the Issuer's location cache. Boot token is
  Provisioner-local (`54n` unchanged). Cold cache after a deploy: WAN
  `/.well-known/…` serves the current `speak-as`.

## Loose threads

- `359.8.2.1` is still `in_progress` — deliverable exists; close when
  the owner is happy with ADR-0024.
- Carried into implementation, not blocking: the exact `#mint-device`
  payload (which ADR-0012 approval message Enroll forwards; Issuer's
  own replay check vs Enroll's single-use nonce).
- Exploration-log §P0.1 and §P0.3 look resolved by ADR-0022/0023 —
  deletion offered, awaiting the owner.
- Domain-model §2 "Policy: payload, not identity" render diagram is now
  superseded twice (ADR-0017, `mdv`); redraw when `359.8.2.3` lands.
- Two unchosen protocol numbers with no bead: `DefaultMailbox = 64`,
  renewal-beat fraction (surfaced as a broken window; ruling pending).
- Unchanged from the gate: cp1 dials the scratch relay until
  `359.8.2.2` (`kql`); `talos/talosconfig` endpoints stale; NixOS box
  1TB disk full; w1 down (`0q0`, `kso`).

## Suggested next steps

- Protocol pre-work, three small model-first tasks: `xwu` → `kau`
  (spec: protocol ADR-0003) → `7ei`. All in `authorize.qnt` / the actor
  runtime; run `TestFaultPairSweep` after each.
- `359.8.2.2` (embed the relay in the hub, `5gz` shape) is independent
  and ready if you'd rather ship something.
- Then `359.8.1` membership issuance against ADR-0018 + ADR-0024.
