# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-03 — one long session, three arcs:

- **Beads repaired** per `1g9` (local DB had diverged after the Aug-22
  force-push; `bd` had silently fallen back to an empty default DB).
  Bootstrapped from `refs/dolt/data`; two real local-only issues
  re-created (`px2`, `359`). Salvage at
  `~/.local/share/beads-salvage/talos-config-2026-09-03/`.
- **Mesh v3 pivot committed** — ADR-0016 (Accepted, gate-bound),
  decision `dlk`, goal added, `docs/mesh-v3-iroh.md` committed. Epic
  `359` expanded into a phase tree; nebula-era backlog deferred on the
  Phase 0 gate, not closed. Decision `5w1`: **monorepo with the
  sovereign-actor protocol at the center**, talos-config = consumer 1
  (restructure task `k3o`, ADR-0018 reserved).
- **Authority model pinned** (grill-me → grill-design on `359.2`,
  closed): vocabulary in `domain-model.md` §"The three layers" (actor /
  authority / negotiation; sovereign = root only; no presence
  concept; network layer ≠ app layer); **ADR-0017 (Proposed)**:
  caller-carried delegation certs, policy compiles to grants, groups
  are issuer-scoped `aud` names, explicit consent grant, `authorize()`
  once per stream; invariants 1+2 amended (*the grant is the record*;
  *git is compiler input, never verifier input*). Economics for v0:
  no per-message money, Base/EVM-L2 for per-relationship flows.
  Contradiction sweep done: mesh-v3 doc, ADR-0016/0014/0007 headers,
  sketch cert encoding, `mesh-policy.yaml` comment, `359.*` titles.

2026-09-04 — backlog grooming (beads only, no code): protocol spike
`8jf` crystallized into epic **`0bc`** (M1–M5 chain, `0bc.1` = cert
primitive, shared with `359.8.1`); `px2` closed (ADR-0015 impl → `359.8.3`,
nebula fallback `7ci` deferred on the gate); `rc3` closed → decision `gar`
(hub fungibility test). `359.4/.6/.7` retyped spike; `4te` → P2; `fbb`
+ `4zt` deferred to `359.8.2` / `359.9.1`; `ihn` now blocked on `359.11.3`;
`k3o` title fixed (ADR-0018). Taskwarrior leftovers closed — migration
complete for this repo.

## Loose threads

- **ADR-0017 is Proposed** — promote when `359.8.1`/`359.8.5` are
  built against it. Until then the domain model's §2 policy diagram
  is explicitly nebula-era.
- The decision trigger for v3 (`dlk`) and the monorepo decision
  (`5w1`) both assume sovereign-actor work *actually starts*; ADR-0016
  §Confirmation says the migration-for-elegance objection returns if
  it stalls.
- `fbb` (deploy re-seals hub) gets heavier under v3 (relay identity
  derives from master) — priority unchanged, revisit at `359.8.2`.
- Open Q-threads: `359.4` desktop presentation, `359.6` hub HTTP
  surface (partly answered: facet `hub-http`; public HTTPS must
  remain for provisioning, invariant 4), `359.7` SideroLink vs node
  agent — must land before `359.8.3`.
- `siweoidc` hardcodes `groups: ["admins"]` (`5kh`, P3).
- Older: domain-model "Laws" section unwritten; `nebderive.DeviceKey`
  moot if v3 passes.

## Suggested next steps

- **Phase 0 spike**, cheapest-kill-first: `359.1.4` API-churn probe →
  `359.1.1` relay on fly → `359.1.3` Talos extension → `359.1.2`
  Android 80 Mbps.
- Or start `0bc.1` (protocol M1, alongside `k3o`): the cert primitive + `authorize()` as a Go
  package with the five properties as a rapid suite — the spec is
  complete (glossary Verb/Grant/Attenuation/Authorize + ADR-0017) and
  it is shared by Mesh v3 P1.1 and the protocol.
- Session-close check: `git ls-remote origin refs/dolt/data` must
  move after `bd dolt push`.
