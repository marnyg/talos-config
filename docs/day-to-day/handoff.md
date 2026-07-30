# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-30 (evening) — **Phase 1 is fully closed.** The last gate —
criterion 4's phone half — was measured and passed (decision c4f07507).

- **Phone enrolled** as a media device (`fly.toml` `MESH_MEDIA_DEVICES`,
  commit `4e8ef8e`): self-served through the `/mesh/tv` device flow on
  the phone's own browser (QR → wallet approval → config over its own
  TLS session). First real use of that flow for a phone.
- **Verdict, out loud:** Mobile Nebula import UX is *bad* but the phone
  is on the mesh — below criterion 4's "would stay off" bar. Driver 2
  stands for phones. UX improvement filed as a `+later +nice` task
  (recurs at the 90-day device-cert renewal, so it may earn priority).
- **Office MacBook decided** (decision d8a8ed86): keeps the shared
  `laptop` identity, leave as-is; revisit only if it becomes a problem.
  Revocation route remains `talos/mesh-blocklist.txt`.
- Closed threads: dba0c63d (TV onboarding — nothing left after the TV
  decision + deploy), d14514fa (criterion-2 office punch test — resolved
  into ADR-0006 earlier).
- `docs/mesh-v2-nebula.md` updated: all four kill criteria settled, the
  "open decision" paragraph resolved.

## Loose threads

- A brief seal window per deploy: the phone-declaration deploys re-sealed
  the hub twice; each needed a wallet unseal at `/status`. Known
  behavior, but phase 2's early steps involve more deploys — budget the
  unseals.
- Thread a7920bda (wallet-authorized auto-enroll for undeclared names)
  is untouched; today's flow — edit fly.toml, deploy, unseal, enroll —
  is exactly the friction it describes.

## Suggested next steps

- **Start phase 2** (task 1afafb50), in order: (1) certSANs (nebula
  name + IP — additive, safe); (2) cluster endpoint → nebula IP,
  re-point talosconfig/kubeconfig (retires the cp1 lease-drift scan
  dance); (3) strip wg0 from compose, delete hub wg* code (closes
  invariant 5's dual-overlay exception).
- Verify the phone shows up in the `/status` mesh table and Jellyfin
  plays over the overlay from a remote network (expect relay parity,
  ADR-0006).
