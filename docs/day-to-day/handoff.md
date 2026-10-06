# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-06: three spikes answered by a swarm (one `claude-fable-5-1`
worker each, herdr worktrees, memos under the new `docs/spikes/`), then
ruled with the owner. Rulings are on the beads' notes; memos are the
analysis.

- **`i1il` app sign-in from mesh identity** → `docs/spikes/
  auth-mesh-identity.md`. The memo's bare-header group gate was
  **rejected**: it rests on reachability and any pod can forge it.
  Ruling: the gateway mints a **signed per-request identity token**
  (`X-Mesh-Token`: device/name/groups, `aud`=Host, `exp` 60 s, member-
  key EdDSA); the bridge serves `/authz` for nginx `auth_request`
  (valid → 200; else 401 → SIWE) and `/authorize` honours the same token
  (zero-click OIDC for ArgoCD/Jellyfin-SSO). oauth2-proxy retires.
  Device ≠ person stays (`5kh` is a prerequisite); splices carry nothing
  (`95la` un-deferred, see below). Task **`a0ys`**.
- **`9z4e` HTTPS over the mesh** → `docs/spikes/tls-over-mesh.md`.
  Plain HTTP breaks nothing deployed. A wallet-rooted mesh CA is ruled
  out both ways (ADR-0018). Ruling: **do nothing until an app forces
  it**, then device-local termination + name-constrained per-device CA
  in the daemon (option E); `goals.md`'s stale "wallet-derived CA" line
  reworded.
- **`kanr` agentic workloads** → `docs/spikes/agents-kagent.md` +
  drafts in `docs/spikes/agents-kagent/`. google/ax ruled out (no
  OpenRouter, not CRDs, needs Agent Substrate). Ruling: **kagent 0.10.3
  trial** (OpenRouter-only, no GPU); Agent Substrate (gVisor sandboxes)
  is worth the k8s ≥ 1.37 + gvisor-extension upgrade only when an agent
  needs code execution *and* kagent 1.0 is GA.
- `auth notes` (and its `~` backup) deleted: content folded into the
  i1il memo's "Identity model" section.

## Loose threads

- **`95la` should be un-deferred**: the i1il memo does not deliver the
  "appliances skip Quick Connect" prize (splices have no headers). Not
  done this session — owner's call.
- ADR-0032 (signed-token app seam) and ADR-0033 (device-local TLS, no
  mesh CA) are drafted as **Proposed**; flip to Accepted when `a0ys`
  lands / when the HTTPS direction is first exercised.
- `a0ys` has a prerequisite in `5kh` (groups hardcoded `["admins"]`).
- Spikes `i1il` / `9z4e` / `kanr` closed 2026-10-06 with rulings on
  their notes; `95la` un-deferred (appliance login is Jellyfin-local).
- herdr: the nix profile has herdr 0.9.1 (protocol 22) while the running
  server is 0.8.2 (protocol 20). This session drove the swarm with the
  store's `/nix/store/2qd228lh…-herdr-0.8.2/bin/herdr`; a herdr restart
  fixes it (kills every pane). See `notes.md`.
- Standing from 10-05: "no NFS client on nas1" is only per-Deployment
  affinities; `transfer` SMB write ceiling unobserved; `jlgz` open.

## Suggested next steps

- Start `a0ys` (gateway token + bridge `/authz`) after fixing `5kh`;
  or rule `95la` (per-device Jellyfin accounts vs Quick Connect).
- kagent trial: seal a spend-limited OpenRouter key, `git mv
  docs/spikes/agents-kagent k8s/apps/kagent`, verify at
  `kagent.gw.mesh.internal` behind the wallet.
- `bsj` (Longhorn backup target) still the storage follow-up.
