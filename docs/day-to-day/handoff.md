# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-04: **nas1's bays are a mirrored bulk tier, and the media
library moved onto the cluster.**

- **`lug3` closed**: `u-longhorn-1/-2` (two 4 TB Toshibas, selected by
  WWID because they report no serial) declared in nas1's `patch.yaml`
  (`a027dd1`). Hub redeployed and unsealed, and `apply` ran with no
  reboot. They are Longhorn disks `bulk-1/-2`, tagged `bulk` (the NVMe
  is tagged `nvme`), added to the Node CR by hand.
- **`longhorn-bulk` = mirror inside nas1** (`2918135`, `6119ccb`; ADR-0029
  *Proposed*): 2 replicas on different bulk disks, with the
  share-managers pinned to nas1. With them on w1, the library had been
  crossing w1's USB dongle twice.
- **Library imported** from the old docker host: 466 GB tv+movies by
  rsync through `scripts/media-import.yaml` (deleted after). Sonarr's
  29 series re-added by API, and all 246 episode files matched. Jellyfin
  was rescanned. App configs and downloads were not migrated (the owner
  calls them expendable). PVCs are now 900/400/200Gi.
- ADR-0028 → Accepted. Filed spike `r4fw` (hub reading `talos/` from
  git at runtime) and bug `vzbf` (gateway panic).

## Loose threads

- **`vzbf` (P1)**: the gateway crash-loops when a proxied WebSocket
  (Sonarr/Radarr SignalR) hits `Raw.Read` after Close in
  `iroh-transport`. Every `*.gw.mesh.internal` name flaps meanwhile.
- **`jx78`**: the default `longhorn` class is not fenced to `nvme`, so
  app-state replicas can land on spinning disk.
- `cnb5`: placement half done. Left: show faulted volumes on `/status`.
- Old docker host: media containers stopped, not removed. Its disk is
  the library's only second copy (`notes.md`).
- Two Sonarr series have no files ("So I'm a Spider", "Interspecies
  Reviewers"). They weren't on the old disk either.
- `installMAC` → `spvd`; agent fixes → `9af0` (unchanged).

## Suggested next steps

- `vzbf`: fix the gateway panic (closed flag in `Raw`), run
  `scripts/test-iroh.sh`, roll the gateway image.
- Review ADR-0029; then `jx78`.
- Owner `todo` leftovers: torrent/seerr/syncthing/sillytavern, Windows
  PC as compute node.
