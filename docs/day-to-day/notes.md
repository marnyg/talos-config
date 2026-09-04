# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review).

     Pruned 2026-09-03: wg0-era and nebula-phase-1/2 history removed
     (struck-through entries live in git history before this date).
     Reference beads issues by id (`talos-config-xxx`). -->

## Read first

- 2026-09-03 — **Read `desired-state/domain-model.md` §"The three
  layers" before any authority/identity discussion.** A design session
  lost an hour to "sovereign" applied to members and an invented
  "presence" concept; both are defined/retired there. ADR-0017 is
  *Proposed*: the running system is still nebula's receiver-side
  firewall, and `mesh-policy.yaml`'s nebula render is what executes
  until Mesh v3 Phase 1. Don't "fix" nebula code toward ADR-0017.
- 2026-09-03 — **Mesh v3 is live in the tracker, not in code.** Nothing
  about the running system changed; nebula is the mesh until Phase 4.
  Deferred nebula-era issues (`cjo en6 4ns 41b 6gq ap2 90a`) are parked
  on the Phase 0 gate — "deferred" ≠ "abandoned"; do not close them
  before `talos-config-359.1.5`.
- 2026-09-05 — **ADR-0017, ADR-0015 and the glossary contain sentences
  the Quint models refute** (`3cx z1z sqm xwz vzj`; FINDING blocks in
  `verification/quint/{authorize,runway,approval}.qnt`). Until ruled
  on, treat the models as the sharper spec: authorize step (2b),
  6-day invoke runway measured as starvation, per-process token
  single-use. `check.sh` asserts the refuted claims *stay* refuted.

## Hub / mesh (nebula, as running)

- Every fly deploy **re-seals the hub**: derived roles (mesh CA, KMS,
  enrollment, DNS) are down until a wallet unseal at `/status`. The
  mesh HTTP listener exists only post-unseal. `/sealed` returns **503
  on mesh startup failure** but never blocks the unseal itself (KMS
  rides the WAN, invariant 4). (`talos-config-fbb`; gets heavier under
  Mesh v3 — relay identity derives from the master.)
- After a hub redeploy + unseal, cp1 is unreachable over the mesh for
  ~45–60 s (lighthouse re-registration + fresh handshake), then
  recovers unaided. Don't page on the first failed ping; warm with
  `ping 10.42.218.125` before `apply`.
- The hub **re-mints its own nebula leaf at every unseal** — never pin
  the hub's leaf fingerprint; pin the CA (`MESH_CA_PIN` in fly.toml,
  derived CA `b881d6ff…`). A wrong-wallet unseal fails loudly.
- A node's overlay firewall lives in its *stored config*: changing
  policy does nothing on nodes until `nix run .#apply`; devices pick it
  up on re-enrollment or `/policy` poll. A `/policy` overlay never
  changes the hub's *own* running firewall (hub scope renders at
  unseal) and every deploy drops the overlay — export first if it
  should survive.
- A mesh derivation error (address collision, bad `meshIP`) refuses
  the whole `/config` serve, provisioning included.
- nebula cert-version skew: anything minting/consuming mesh certs must
  be ≥ 1.10 (V2 certs); the hub embeds 1.11.0. Also in
  `technical/guides/gotchas.md`.
- **Any overlay carrying the route to the hub/peer poisons a punch
  measurement** (Tailscale exit node, another VPN) — nebula hairpins
  through it. Pre-flight: `route get <peer-ip>` (macOS) / `ip route
  get` must show a physical NIC.
- Home network has **no native IPv6** — the blocker on ADR-0006's
  revisit trigger (`talos-config-41b`, deferred under v3).
- The office MacBook and the home laptop are both enrolled as device
  name `laptop` — same address; do not run both simultaneously.
  Decided to leave as-is; revocation path is `talos/mesh-blocklist.txt`.
- **ADR-0012 is live**: enrollment mints only device-born keys.
  Pre-ADR master-derived device certs stay valid until their 90-day
  expiry. Re-enrolling under the same name keeps the same address.
- Stock Mobile Nebula accepts the hub-issued yaml (move yaml + key
  onto the phone; renewal = re-import every 90 days —
  `talos-config-0qq`, resolved in design by ADR-0017).

## Cluster / Talos

- **cp1's LAN lease drifts freely** (moved four times in one day).
  Never hardcode it; the cluster endpoint is cp1's mesh address
  `10.42.218.125`. Find a node in maintenance mode with a port-50000
  scan. (Mesh v3 P2.5 replaces this with declared static LAN IPs.)
- Talos-generated node names are **not stable across reinstalls**
  (cp1 is currently `talos-wu6-eib`); w1 pins its hostname for this
  reason.
- Standard reinstall is `reset --system-labels-to-wipe STATE
  --system-labels-to-wipe EPHEMERAL` (bootloader survives, node returns
  in maintenance mode); a plain `talosctl reset` wipes the whole disk.
  See `technical/guides/reinstall.md`.
- `talosctl wipe disk <part>` does **not** free a user volume;
  `--drop-partition` is the real flag. Partitions renumber.
- Talos enforces the `baseline` Pod Security Standard on workload
  namespaces: hostNetwork/hostPort pods are silently forbidden (error
  only in `describe ds` events). Fix: `pod-security.kubernetes.io/
  enforce: privileged` namespace label via ArgoCD
  `managedNamespaceMetadata` (ingress-nginx has it).
- A machine waiting on device-flow approval has **no apid** (port
  50000 refused) — hardware is un-inspectable until approved. The hub
  logs mac + uuid + serial at `/device/code` time: `fly logs | grep
  "device auth started"` is how you write `meta.yaml`.
- `install.disk: /dev/sda` is a landmine on USB-booting boxes (w1);
  removing the key needs an RFC6902 patch (merge patches can't
  delete; `disk: ""` is silently dropped).
- A worker cannot reuse the cluster layer: workers take
  `worker-cluster.yaml` + `worker-secrets.yaml`; regenerate the latter
  with the `yq del(...)` one-liner in its header when `secrets.yaml`
  changes.
- `talosctl` flags are not global: `-n`/`-e` follow the subcommand;
  maintenance-mode reads are `get -i`, not `--insecure`.
- `~/.kube/config` is dead (routes through removed zitadel +
  kube-oidc-proxy); use `--kubeconfig ./kubeconfig` from the repo root.

## Workloads / storage

- **w1 is down** (since 2026-08-04; no LAN ping, no apid). Media
  library offline — all `longhorn-bulk` volumes `faulted` (single
  replica on w1). Needs physical attention; data presumed intact. Do
  not start storage work until it returns (`talos-config-kso`,
  `talos-config-0q0`). win2k25 system/ISO volumes are `degraded` (1 of
  2 replicas) and heal unaided when w1 returns.
- **Knowing deviation from invariant 2**: `longhorn-bulk` runs 1
  replica — the media library is neither git-derivable nor
  replicated. Accepted only until the new nodes land
  (`talos-config-0q0`). Wrong implementation, not a relaxed invariant.
- **Nothing app-level is replicated**: apps keep config on `emptyDir`;
  state dies on pod restart. "Longhorn is up" ≠ "state is safe".
- **Pods must not dial `.mesh.internal` names** — nebula routes
  10.42.0.0/16 sources, a pod's source is 10.244.x.x, so it only
  works while the pod lands on cp1 (70-min SSO outage, 2026-07-31).
  Rule: pods talk to Services; the mesh is for hosts and browsers.
  The issuer name is aliased to the siwe-oidc Service ClusterIP via
  `hostAliases` on every relying party (ADR-0010).
- ArgoCD polls git every ~3 min; nudge with `kubectl annotate
  application apps -n argocd argocd.argoproj.io/refresh=normal
  --overwrite`. Patching upstream-owned resources from git = SSA
  partial manifest with `argocd.argoproj.io/sync-options:
  ServerSideApply=true,Validate=false` (see
  `k8s/apps/argocd/server-patch.yaml`).
- ArgoCD-managed Jobs: never set `ttlSecondsAfterFinished` (prune-
  recreate loop); Jobs are immutable — bump the name on script change.
  `virtctl stop/start` is futile under selfHeal — VM state is a git
  edit of `runStrategy`.
- siwe-oidc: a bridge restart rotates the JWKS → ArgoCD sessions die,
  oauth2-proxy sessions survive. The image is `:latest` +
  `imagePullPolicy: Always` — CI push changes nothing until
  `kubectl -n sso rollout restart deployment siwe-oidc`.
- win2k25 reinstall is one API act: `scripts/win2k25-reinstall.sh`;
  RDP back ~25 min later. Password in secret `win2k25-admin`; rotating
  it requires resealing both that secret and the autounattend secret.
- jellyfin's admin password: `kubectl -n media get secret
  jellyfin-admin -o jsonpath='{.data.password}' | base64 -d` (the new
  pod is blocked on the faulted media volumes until w1 returns).

## Android app

- Distributed via the rolling `android-latest` GitHub release; every
  push touching `android/` or `config-server/{mobile,devkey}`
  re-clobbers `talos-mesh.apk`. CI is the only builder (no local SDK).
- The app pushes **no DNS server to the VpnService** (Android sends all
  device DNS to a VPN resolver; the hub only answers the mesh zone).
  Mesh names don't resolve on the TV; services are reached by IP from
  the app's host list. Superseded under Mesh v3 by client-side fake-IP
  resolution (`talos-config-359.9.4`).

## Tooling

- `nix build .#config-server-bin` runs the full go test suite in its
  checkPhase: any failing test on HEAD blocks devshell rebuilds and
  deploys. The devshell's quint is 0.30.0 (flake pin) vs 0.32.0 on
  current nixpkgs — models run on both; `verification/quint/
  _apalache-out/` is gitignored scratch.
- 2026-09-05 — `nix develop` needs `--impure` (devenv: "was not able
  to determine the current directory" otherwise). `check.sh verify`
  takes ~3 min, dominated by `approval.qnt` at depth 12.
- 2026-09-05 — **Quint laws over many nondet dimensions hold
  vacuously.** `authorize.qnt` with ~40 independent `oneOf`s reached
  Accept with p≈2⁻¹⁵; all 7 seeded mutants survived until a
  boundary-biased generator (valid scenario + ≤2 injected faults)
  was added. Always mutation-test a new model before trusting `[ok]`.
  Also: never `powerset()` a 25-element set in a generator.
- **`pi -p` hangs after completing its answer** when stdin is left
  open. Wrap non-interactive uses: `timeout -k 10 420 pi -p
  --no-session "…" </dev/null`.
- **Beads config lives outside git and can vanish.** `.beads/
  config.yaml` + `metadata.json` are gitignored; if missing, `bd`
  silently falls back to an empty DB named `beads` and reports "no
  open issues". Check `bd dolt show` says `Database: talos_config`.
  `export.auto` is `false`. Session-close check: `git ls-remote origin
  refs/dolt/data` must move after `bd dolt push`. Q-threads that are
  `blocks`-chained need `--force` to close with a reason.

## Unverified leftovers (from the 2026-07-24 handover; ask before filing)

- KMS slot 0 is never used at boot (early-boot DNS loses the race).
  Accepted; a KMS endpoint by IP would dodge DNS.
- Old kubeconfigs pointing at `10.0.0.x` are dead — regenerate.
