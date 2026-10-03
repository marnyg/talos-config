# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 is reached** (2026-09-21, Phase 4 closed) and the
fleet is **three nodes** since 2026-09-22: nas1, the storage node,
provisioned end-to-end through the declared path (MAC-selected config,
one wallet approval, key minted on the machine) with no step done by
hand. No nebula anywhere; the identity plane is the only plane. The
epic `talos-config-359` stays open only for field items that are not
the goal: parents' TV (`4te`, ADR-0013's gate), stale `enrollmsg` v2
binaries (`5q33`), relay access gating (`5gz`), `bh74`.

**Next candidates** (owner to pick):
- **Storage tiers**: nas1's two 4 TB bays form the `bulk` tier, and
  the media library mirrors across them. Its NFS servers are pinned to
  nas1, and it was imported from the old docker host (2026-10-03,
  ADR-0029 Proposed). App state (sonarr/radarr/jellyfin/transmission)
  reached the default class the same day (`vu9n`) and sonarr/radarr
  took the docker host's DBs with it. Left: fence the default class
  to `nvme` (`jx78`), show faulted volumes on `/status` (`cnb5`),
  retire the old docker host. Then the owner's app list (seerr,
  syncthing, sillytavern).
- **Gateway**: the WebSocket-after-Close panic (`vzbf`) is fixed and
  rolled 2026-10-03; what remains is the iroh-ffi read-cancellation
  thread (`vh6e`, P3), not a goal.
- **HA sweep `9l67` closed 2026-10-01** (a dead node no longer
  freezes GitOps or pins RWO volumes; `/status` watches ArgoCD;
  ingress-nginx + oauth2-proxy are 2× anti-affine, siwe-oidc fails
  over in ~1 min; gateway stays stateful by decision `nfmt`, 30–60 s
  failover accepted). Left: `jko0`, the control-loop pod pass.
- **Sovereign-actor protocol v0** (`0bc`, M1–M5) — **M1–M4 built;
  M4 accepted live 2026-09-29** (`0bc.4.6`: laptop parent → k8s
  provisioner in ns `sap` + a docker one; ADR-0008/0009 Accepted).
  Next: M5 money (`0bc.5`) or lighthouse discovery for provisioners;
  talos wire unchanged throughout. State and history live in
  `protocol/docs/day-to-day/`. The talos Provisioner's split
  along ADR-0009 is thread `kckm`, not v0.
- **Disk-secret hygiene**: `installMAC` grandfathers the fleet's slot-1
  passphrases (ADR-0028, 2026-10-03); the exit is proving slot-0 KMS
  unlock at boot (`spvd`). Ship the agent's expired-speak-as fix with
  the next extension build (`9af0`).
- Small ops: cp1 hostname pin (`t7b2`), SA-issuer runbook done (`etzl`).

**Out of scope:** wallet-native app sign-in (`95la` behind spike
`i1il`); KMS onto 443 (`os8s`); the daemon's control socket (`fgr`).
