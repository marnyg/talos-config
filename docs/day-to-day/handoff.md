# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-08-05 (seventh session) — design session, no code: **ADR-0012
drafted** (`technical/adrs/0012-wallet-signed-device-enrollment.md`,
Proposed). Wallet-signed enrollment replaces the declared device list;
device-generated keypairs (no key ever travels); approver-set
name+group; two entry modes (nebup direct / RFC-8628) over one
verify+mint core; live-peers-only device DNS; `/config` gate =
`groups ∋ admins` + derivation-consistency. Client scope: nebup loses
the admin-mediated transfer mode, gains `-group` (default `admins`)
and a two-file cache (`<name>.key` persists, `-rekey` rotates);
approval extends `/status` (no new page); no Android code now — TV
APK (`2e1bef85`) stays deferred, spec re-annotated to the unified
flow. Invariant 1 amended (uncommitted alongside the ADR).

## Loose threads

- **w1 still down** (since 2026-08-04 ~09:26) — physical power/console
  check; everything storage-shaped waits on it. Old jellyfin pod runs
  admin/admin until it cycles (new password:
  `kubectl -n media get secret jellyfin-admin …` after w1 returns).
- **ADR-0012 is Proposed, not Accepted**; implementation not started.
  The exploration-log section stays until the ADR is accepted.
- **Phone enrollment is accepted-broken until the APK exists**: mode 3
  (transfer-a-config) dies with the implementation, and the Mobile
  Nebula import path is unverified for self-generated keys. Owner
  accepted this explicitly.
- Open design items: signed-message prefix versioning; 90-day renewal
  automation (persistent device key makes "re-sign same pubkey"
  possible in principle).
- ArgoCD wallet login still not browser-tested since the `fed04b4` fix.

## Suggested next steps

- Power-cycle w1; verify volumes leave `faulted`, jellyfin's new pod
  starts, ArgoCD wallet login works.
- Accept ADR-0012 (flip status) and commit the docs bundle.
- Start implementation hub-side: one verify+mint core + the new signed
  message, then nebup rework, then the `/status` approval form.
