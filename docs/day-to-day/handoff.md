# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-03 — **w1 back, media library recovered, Linux desktop client,
break-glass passphrase re-rooted.**

- **cp1 had dropped its ethernet link** (no ARP at `10.0.0.68`, API
  down, a transient `address-overlap` diagnostic on its console). A
  replug fixed it; no reboot, no unseal. Noted in `notes.md`: this
  box (`mar@nixos`, LAN `10.0.0.11`) can run `talosctl -e <LAN IP>`
  with the owner talosconfig when the mesh is down — invariant 4 held.
- **w1 returned** (`10.0.0.71`, dongle MAC). Untainted; Longhorn came
  back on it and `media/{tv,movies,downloads}` went faulted → healthy
  (`cnb5`, ~12 d outage, decision halves still open). Re-served
  (`apply`: boot token + Longhorn label, **no reboot**) and back on the
  mesh as `w1.mesh.internal`, same NodeId — after a workaround (below).
- **`irohup -tun` on Linux** (`38882a9`): `fakeip/tun_linux.go`
  (fixed tun `talosmesh0`, `ip` for addr/route, zone declared per-link
  via `resolvectl`), `cmd/irohup/tun.go` shared. NixOS module
  `modules.nixos.services.talos-mesh` in `~/git/nixos` (desktop host,
  user `talosmesh`, unit gated on `kit.json`, `talos-mesh-enroll`).
  Live here: `nix run .#apply`, `talosctl -e cp1.mesh.internal`,
  `curl http://hub.mesh.internal/config?mac=…` all work from this box.
- **Break-glass passphrase (LUKS slot 1) derived from the directory
  MAC** — `c4vd`'s rename rotated w1's composed passphrase unnoticed;
  the re-serve dry-run showed it with "with a reboot". Fixed
  (`17b3a37`, hub deployed + unsealed): `RecoveryPassphrase(master,
  uuid)` v2; `installMAC:` in `meta.yaml` grandfathers cp1/nas1/w1
  (`RecoveryPassphraseMAC`, frozen); `TestDiskEncryptionSurvivesRename`;
  `apply` dry-runs first and refuses an encryption diff outright, a
  reboot without `APPLY_REBOOT=1`. `recover -recovery -uuid|-mac`.
  Rule and exit in `deployed-state.md` (disk encryption posture) and
  ADR-0028 (Proposed).
- **Agent bug** (`7fb6473`, not yet shipped — `9af0`): a kit with a
  live member cert under an **expired speak-as** passed the local
  check and looped on `ErrChainExpired` instead of redeeming the fresh
  boot token. Fixed in `nodeagent` + test ("attic" kit); on w1 worked
  around with `kubectl debug node/w1 … rm /host/var/lib/p0agent/
  {kit,bundle,hub}.json` + `talosctl service ext-p0agent restart`.
- `TestNodeAgentEndToEnd`'s redeploy scenario was racy by construction
  (live desk + `connLost` kick vs. "still holds the dead key"); now a
  closed-before-redeploy **sleeper** pins the dial path deterministically.

## Loose threads

- **nas1's two 4 TB disks are visible** (`sdd`/`sde`, Toshiba MN10ADA4)
  and undeclared — `lug3` (P1). Then `cnb5`'s replica-placement half.
- `installMAC` is transitional; its exit is `spvd` (prove slot-0 KMS
  unlock at boot under v3, then one reboot per node re-keys).
- Agent fix `7fb6473` reaches the fleet only via a p0agent extension +
  installer + `talosctl upgrade` (reboot each) — `9af0`.
- The Mac still runs the pre-`38882a9` daemon; fine (same wire).
- Proposed, not written: invariant 7 amendment and two domain-model
  edits (see the docs-update report in the session log / ADR-0028).

## Suggested next steps

- `lug3`: `UserVolumeConfig` for nas1's disks, `apply` (watch the new
  guard), then decide `longhorn-bulk` placement.
- Review/accept ADR-0028; decide on the invariant 7 wording.
- `spvd` before any further `systemDiskEncryption` change.
