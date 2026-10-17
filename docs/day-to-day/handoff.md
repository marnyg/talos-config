# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session
2026-10-22: **ArgoCD became an app of apps** (`pe8m`, `8045820`;
ADR-0040 Accepted). The root `apps` Application now syncs
`k8s/clusters/homelab/` — an ApplicationSet (list generator) that
emits one `platform-<name>` Application per `k8s/apps/<name>`
directory, plus AppProject `platform` (everything) and an emptied
`default` (fails closed). The six nested Helm Applications moved to
`platform`. Children are plain directories (no kustomize, no
overlays — one cluster), uniform template: automated prune+selfHeal,
retry 10×/3m, `SkipDryRunOnMissingResource`. Installer Job applies
the `platform` AppProject from the raw git URL before the root.
Live cutover: root re-applied with `prune: false`, 37 children +
6 nested adopted every resource in ~30 s, zero left on
`instance=apps`, no pod bounced, prune re-enabled. `1ova` is closed
by construction (a stuck `platform-vms` holds only itself). Shape
from mono-dees-nuts' `clusters/<cluster>/cluster.yaml`.

Previous sessions (2026-10-17→21): CI on the cluster feeds the Nix
cache (`0m50`, ADR-0039; nine runs, fixes in `git log` and the
image.nix/attic-config comments); Actual Budget (`7yzv`); the admins
gate became ingress-nginx's `global-auth-url` (opt out with
`enable-global-auth: "false"`); Audiobookshelf + Chaptarr (`htsj`).

## Loose threads

- **Adding a workload now needs a line in
  `k8s/clusters/homelab/cluster.yaml`** as well as the directory; a
  directory without a line is silently not deployed. Removing a line
  deletes the directory's resources (ApplicationSet finalizer).
- The hub's gitops row (`/status`) watches only the root `apps`; a
  stuck child sync — the `9l67` symptom — is invisible to it (`2c2x`).
- `deployed-state.md` ArgoCD line and the 2026-09-29 terminate recipe
  in notes.md describe the pre-AoA shape; the recipe still works per
  child (`argocd app terminate-op platform-<name>`).
- `3leu` (debt): the sandbox agent's Forgejo token header still sits
  on a disk-backed emptyDir (invariant 8, same fix as the runner).
- The runner image builds on the laptop path (`build.sh`, box via
  `HUB_BUILDER`); CI caches it but doesn't push to ghcr.
- Darwin outputs (the Mac) are still built by no one. The Mac needs the
  nix.conf from `nix-cache/bootstrap.md` §3 to use the cache at all.

- Actual: OpenID against the SIWE bridge (`ACTUAL_LOGIN_METHOD=openid`)
  is the path if the household ever needs per-person accounts; not
  wired.

- **Chaptarr first run in the UI:** root folders `/books/audiobooks`
  and `/books/ebooks` (per-format profile object, not scriptable before
  first start — see `chaptarr/configmap.yaml`), with "write
  Audiobookshelf metadata" on. **ABS first run:** create the admin
  user, add a library at `/books/audiobooks` (and one for ebooks).
- Prowlarr still holds **no indexers** (added in the UI, none yet), so
  Chaptarr gets only NZBgeek until that happens. Chaptarr's Prowlarr
  PUT once failed transiently right after a pod restart (Prowlarr
  validates against a Chaptarr still booting); the next configurator
  run healed it. The PUT paths in all four *arr configurators have no
  retry, only the POSTs do.
- ABS 2.37 serves with `ROUTER_BASE_PATH` defaulting to
  `/audiobookshelf` and rewrites `/` into it internally; `/` works
  through the ingress, but if the apps misbehave, set the env to `""`.
- ABS OIDC against the SIWE bridge (PKCE, no secret) + an ungated
  Ingress is the path if household devices should listen; not started.
- From 10-17: HA onboarding/companion app, `b311`; hub image on fly
  still pre-pump `facethttp` (next `fly/deploy.sh` picks it up);
  Vaultwarden live sync / Grafana live untested after the gateway roll.
- From before: `thz2`, `m6xc`, `d0o0.*` / `e6pu.2.*` awaiting close,
  9+ `<!-- stale? -->` notes.

## Suggested next steps

- Point the NixOS box's and the Mac's
  nix.conf at the cache (`nix-cache/bootstrap.md` §3) and watch a
  `nixos-rebuild` substitute instead of build.
- Do the two first-run setups above, drop one audiobook in, confirm
  Chaptarr import → ABS scan end to end; then close `htsj`.
- Add indexers to Prowlarr so the pairing carries something.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
