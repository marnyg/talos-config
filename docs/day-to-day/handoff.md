# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session
2026-10-17: **CI on the cluster feeds the Nix cache** (`0m50`, closed;
ADR-0039 Proposed). `k8s/apps/forgejo-runner`: one host-mode
forgejo-runner (`cluster-nix`, image `b60f951`, PSS baseline, nixbld
build users, credentials on memory emptyDirs) runs
`.forgejo/workflows/nix-cache.yml` from the pull-mirror
`mirrors/talos-config` on each push, nightly and on dispatch. It builds
every x86_64-linux package of this flake and the desktop, laptop and
wsl toplevels of `marnyg/nixos`, then pushes them to Attic. Verified:
run #9 green; config-server and all three `nixos-system-*` HIT from the
Mac over the mesh. Getting there took nine runs and five fixes (run
token for the private mirror, honest store registration, build users,
attic endpoint pin, attic chunking off). Each is in `git log` and the
image.nix/attic-config comments. Also: attic memory limit 2Gi, runner
ns warn/audit baseline, invariant 8 enforced on the runner (`7828ce2`).

Parallel session, same worktree (its dates read 2026-10-20/21): **Actual
Budget** (`7yzv`) at `budget.gw.mesh.internal` (Vaultwarden posture,
verified live); **the admins gate became ingress-nginx's
`global-auth-url`**: gated unless an Ingress opts out with
`enable-global-auth: "false"` (argocd, jellyfin, seerr, siwe-oidc);
**Audiobookshelf + Chaptarr** in `media` (`htsj`) on a shared `books`
volume, both 200 through the gateway.

## Loose threads

- ADR-0039 is **Proposed**: the owner accepts it, or not.
- `3leu` (debt): the sandbox agent's Forgejo token header still sits
  on a disk-backed emptyDir (invariant 8, same fix as the runner).
- `1ova` (debt): one unhealthy wave-0 resource holds the whole `apps`
  sync op (vms/ waves). Bit again today; terminate recipe in notes.md.
- The runner image builds on the laptop path (`build.sh`, box via
  `HUB_BUILDER`); CI caches it but doesn't push to ghcr.
- Darwin outputs (the Mac) are still built by no one. The Mac needs the
  nix.conf from `nix-cache/bootstrap.md` §3 to use the cache at all.

- Actual: OpenID against the SIWE bridge (`ACTUAL_LOGIN_METHOD=openid`)
  is the path if the household ever needs per-person accounts; not
  wired.
- ADR-0032 still says "one Ingress annotation per app" for the gate;
  the mechanism is now the controller default (comments in
  `k8s/apps/media/ingress.yaml` and `ingress-nginx/application.yaml`
  are the authoritative description). An amendment line on the ADR
  would close the gap.

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

- Accept (or amend) ADR-0039; point the NixOS box's and the Mac's
  nix.conf at the cache (`nix-cache/bootstrap.md` §3) and watch a
  `nixos-rebuild` substitute instead of build.
- Do the two first-run setups above, drop one audiobook in, confirm
  Chaptarr import → ABS scan end to end; then close `htsj`.
- Add indexers to Prowlarr so the pairing carries something.
- Back to `ivof` (sandbox-agent live acceptance, the parent actor).
