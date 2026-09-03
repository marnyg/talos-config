# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-03 — **Pivot to Mesh v3 (iroh) committed as direction**, and
beads repaired.

- **Beads divergence resolved** per the `1g9` handover: this machine
  had the pre-force-push local DB (demo data + two real issues) and
  `bd` had silently fallen back to an empty default DB because
  `120c73f` deleted `.beads/{config,metadata}` from the worktree.
  Bootstrapped from `refs/dolt/data`, re-created the two real
  local-only issues as `px2` (identity-lifecycle spike, notes intact)
  and `359` (Mesh v3 epic). Divergent DB salvaged at
  `~/.local/share/beads-salvage/talos-config-2026-09-03/`.
- **`docs/mesh-v3-iroh.md` committed** (`497ce65`; had sat untracked
  since 2026-08-17). Visualisation leftovers (`graph.html`,
  `scripts/beads-viz`) deleted.
- **Decision `talos-config-dlk`**: pivot to v3; trigger = sovereign-
  actor work (`8jf`) moving from sketch to build. Epic `359`
  un-deferred (P1) and expanded into a 31-issue phase tree: P0 spike
  gate (4 checks → gate decision) → 6 open-question threads → P1 dual
  plane → P2 per-consumer cutover → P3 soak → P4 deletion, all
  `blocks`-chained per the doc. Nebula-specific backlog (`cjo en6 4ns
  41b 6gq ap2 90a`) **deferred, not closed**, with notes pointing at
  the gate; reshaped issues `related`-linked to their replacing task.

## Loose threads

- `goals.md` gained the Mesh v3 goal and ADR-0016 is Accepted with
  the Phase 0 gate as a binding condition (both written this session);
  `focus.md` ties to the new goal. Exploration-log sections resolved
  by ADR-0010/0013 pruned.
- **Grill session outcomes (2026-09-03, recorded in beads):**
  NodeId *complements* SIWE — network layer = cert chain + policy, no
  per-session login, device custody = access; app sessions stay on the
  SIWE→OIDC bridge (`359.9.3`). Economics: no per-message money in v0,
  Base/EVM-L2 for per-relationship flows, stake-on-first-contact
  dropped (`8jf` notes; sentence added to `sovereign-actor-protocol.md`
  §Economics). **Decision `5w1`: monorepo with the actor protocol at
  its center, talos-config = consumer 1** — restructure task `k3o`
  (protocol package, docs sub-scope, ADR-0017). `8eq` closed.
- **Next design session is `/skill:grill-design` on `359.2`** (P1):
  the permission-hierarchy data structure — delegation cert as the
  single authority primitive, define the `can`/verb vocabulary, map
  groups onto it. Domain model gets the result.
- The Aug-21 loose threads still stand: domain-model "Laws" section
  unwritten; `nebderive.DeviceKey` question undecided (now moot if v3
  passes — `nebderive` is deleted in P4).
- `fbb` (deploy re-seals hub) gets more load-bearing under v3: sealed
  hub = no relay = no remote path for anyone. Priority unchanged for
  now; revisit at `359.8.2`.
- `2y7` (SideroLink) vs the bespoke node agent is an unresolved
  either/or — thread `359.7`, must land before `359.8.3`.

## Suggested next steps

- Start Phase 0: `bd ready` shows the four spike checks (`359.1.1–4`).
  Order of cheapest-kill-first: `359.1.4` API-churn probe (desk work),
  then `359.1.1` relay on fly, then `359.1.3` Talos extension, then
  `359.1.2` Android 80 Mbps (needs the CI APK pipeline).
- Before closing any session: `git ls-remote origin refs/dolt/data`
  vs `bd dolt status` — the divergence above went unnoticed 12 days.
