# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review). -->

- 2026-07-29 — ~~`nix run .#apply` is dangerous on provisioned machines~~
  Fixed later same day (#30): apply now fetches the hub-composed config
  over the tunnel and refuses to compose locally.
- 2026-07-29 — Every fly deploy re-seals the hub: derived roles (wg,
  KMS, enrollment) are down until a wallet unseal at `/status`. The
  tunnel HTTP listener (hello + admin `/config`) only exists post-unseal.
- 2026-07-29 — `nix run .#apply` now requires being on the wg tunnel as
  an admin peer (`wgup`) and an unsealed hub. `APPLY_HUB` overrides the
  default `http://10.99.0.1`.
- 2026-07-29 — `wgping` binaries built before the tunnel-HTTP change
  hang against the new hub (the hello no longer speaks unprompted —
  rebuild wgping).
- 2026-07-29 — nebula cert-version skew: `nebula-cert` ≥1.10 emits V2
  certs by default; nebula ≤1.9 (e.g. alpine's apk package) can't parse
  them. Anything minting or consuming mesh certs must be ≥1.10; the hub
  embeds 1.11.0. (Now also recorded in `technical/guides/gotchas.md`.)
- 2026-07-29 — mesh names resolve but nothing answers on them yet:
  `<name>.mesh.internal` is served by the hub from derived state, while
  machine and device certs are not minted until the node side lands.
  Node-side injection is written as of later the same day, so this holds
  only until the deploy + `talosctl upgrade` + `apply` sequence runs.
- 2026-07-29 — the node's mesh identity arrives with its *config*, and
  the nebula extension arrives with its *installer image*. Both are
  needed: a node on the new schematic without a re-fetched config runs
  nebula with no config file, and a re-fetched config on the old
  schematic carries an ExtensionServiceConfig no service consumes.
  Neither state is harmful, but neither is on the mesh.
- 2026-07-29 — a mesh derivation error (address collision, bad `meshIP`)
  now refuses the whole `/config` serve, provisioning included. Pure
  derivation, so no overlay dependency, but it does mean a repo mistake
  in mesh addressing blocks installs — the same posture wg0 already has.
- 2026-07-29 — the mesh is enabled per-deploy with `--mesh-port` and
  requires `--wg-port` (phase 1 is a dual overlay, one master). A mesh
  startup failure is deliberately non-fatal: `/sealed` reports
  `mesh: DOWN (<err>)` but still returns 200, because wg0 carries
  production traffic while the mesh is on trial.

### Absorbed from the legacy `handover.md` (2026-07-24), still open

These were unchecked loose ends when that file was written; none have
been verified since, so treat them as unconfirmed. Not filed as tasks —
ask before doing so.

- Laptop WG config lives in `/tmp/wg-talos.conf` — move somewhere
  permanent + `chmod 600` (user action).
- `argocd-dex-server` pod in Error — unused OIDC component; candidate
  for disabling in the ArgoCD install.
- `tailscale-operator` Application Degraded — never investigated. Worth
  a look given it may be moot once the mesh lands.
- KMS slot 0 is never used at boot (early-boot DNS loses the race).
  Accepted; a KMS endpoint by IP instead of hostname would dodge DNS.
- Old kubeconfigs pointing at `10.0.0.x` are dead — regenerate.
