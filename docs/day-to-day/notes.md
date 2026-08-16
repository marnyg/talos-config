# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review).

     Reference tasks by 8-char UUID prefix (`85ba4de5`), never by short id (`task 42`):
     short ids are renumbered as tasks complete, so the reference silently comes to
     point at an unrelated task. -->

- 2026-07-29 — ~~`nix run .#apply` is dangerous on provisioned machines~~
  Fixed later same day (`4dcfb659`): apply now fetches the hub-composed config
  over the tunnel and refuses to compose locally.
- 2026-07-29 — Every fly deploy re-seals the hub: derived roles (wg,
  KMS, enrollment) are down until a wallet unseal at `/status`. The
  tunnel HTTP listener (hello + admin `/config`) only exists post-unseal.
- 2026-07-29 — ~~`nix run .#apply` now requires being on the wg tunnel as
  an admin peer (`wgup`) and an unsealed hub. `APPLY_HUB` overrides the
  default `http://10.99.0.1`.~~ Superseded by step 3 (2026-07-30):
  apply rides the mesh as an admin device (`nebup`), default
  `http://10.42.0.1`.
- 2026-07-29 — ~~`wgping` binaries built before the tunnel-HTTP change
  hang against the new hub.~~ Tool deleted with wg0 (2026-07-30);
  offline derivations moved to `cmd/recover`.
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
- 2026-07-29 — a node's overlay firewall lives in its *stored config*, so
  changing `nodeNebulaConfig` (adding the media rule, say) does nothing
  until every node re-fetches and re-applies. ~~cp1's applied config
  predates the media group~~ Re-applied later same day (`84d7ca5`);
  the media rule is live on cp1.
- 2026-07-29 — the laptop's wireguard interface is named `talos-laptop`,
  **not** `wg0` — `ip link show wg0` proving absence proves nothing. It
  is kernel wireguard with no owning daemon (`wg show` output can still
  be empty — check for `kworker/R-wg-crypt-talos-laptop`); take it down
  with `sudo ip link del talos-laptop`, restore with `wgup`.
- 2026-07-29 — **any mesh measurement taken with the wg tunnel up is
  poisoned**: the lighthouse advertises cp1's wg and pod IPs
  (`10.99.0.54`, `10.244.x`) and a dual-overlay client will "punch"
  straight through wireguard — nebula-over-wg-over-fly, double
  encryption, hairpinned. Symptom: handshake `from="10.99.0.54:4242"`.
  Two criterion-2 runs were invalidated by this before it was caught.
- 2026-07-29 — first mesh tunnel takes ~6s to converge: the initial
  pings are dropped during lighthouse query + punch, then arrive relayed
  (~27ms via fly), then direct (~1.8ms on the LAN). Steady-state latency
  is the number that matters; the loss is setup, not packet loss.
- 2026-07-29 — the upgrade→apply gap is safe to sit in: cp1 spent ~5
  minutes with `ext-nebula` in `[Waiting]: Waiting for extension service
  config` and nothing else degraded (apid up, etcd fine, media stack
  untouched). Deploy the extension and the identity in either order; the
  node just is not on the mesh until both land.
- 2026-07-29 — a mesh derivation error (address collision, bad `meshIP`)
  now refuses the whole `/config` serve, provisioning included. Pure
  derivation, so no overlay dependency, but it does mean a repo mistake
  in mesh addressing blocks installs — the same posture wg0 already has.
- 2026-07-29 — ~~the mesh is enabled per-deploy with `--mesh-port` and
  requires `--wg-port`… a mesh startup failure is non-fatal, `/sealed`
  still returns 200.~~ Inverted by step 3 (2026-07-30): `--mesh-port`
  is standalone and `/sealed` returns **503 on mesh startup failure**
  (it still never blocks the unseal itself — KMS rides the WAN,
  invariant 4).

- 2026-07-30 — **Any overlay carrying the route to the hub/peer poisons a
  punch measurement**: nebula will use it as underlay and hairpin. Bit us
  twice with wg0; the office run survived only because Tailscale happened
  to have no exit node set. Portable pre-flight before any punch test —
  `netstat -rn` plus `route get <peer-ip>` (macOS) or `ip route get`
  (Linux) — confirm egress is a physical NIC. The old `ip link` check
  silently no-ops on macOS, which is where these tests actually run.
  Guard filed as a bug against `nebup` (`85ba4de5`).
- 2026-07-30 — **The office MacBook is enrolled as device name `laptop`**,
  the same default the home laptop used. Identity derives from (master,
  name), so both machines hold the *same* mesh key and the same overlay
  address — do not run nebula on both simultaneously. It also means a
  corporate-managed machine holds an owner-device mesh credential;
  revocation route is `talos/mesh-blocklist.txt`. _Decided later same
  day (d8a8ed86): leave as-is; revisit only if it becomes a problem._
- 2026-07-30 — Home network has **no native IPv6** (no v6 default route;
  the only global-scope v6 address is a Tailscale ULA). This is the
  blocker on ADR-0006's revisit trigger for direct remote paths.
- 2026-07-30 — **cp1's LAN lease drifts freely**: 10.0.0.20→.30
  overnight, .30→.31 across a single reboot. `talosctl` contexts and
  muscle memory pointing at a LAN IP are stale within days; find the
  node with a port-50000 scan (`echo > /dev/tcp/10.0.0.N/50000`). Mesh
  and wg addresses were stable throughout. Phase 2 step 2 retires this.
- 2026-07-30 — **Mobile Nebula on the phone was configured by hand**:
  the `/mesh/tv` flow delivers a self-contained YAML, but the app has no
  config-file import — CA/cert/key and lighthouse/relay/static-host-map
  details were entered through its site UI. Workable but clunky; recurs
  at every 90-day device-cert renewal (`nebDeviceCertValidity`). A
  re-enroll mints the same identity, so renewal is re-entry, not
  re-approval of anything new.
- 2026-07-31 — **the cluster endpoint is now cp1's mesh address**
  (`10.42.218.125`): kube API reachability from a node rides nebula0
  existing locally (`ext-nebula` extension service), not a native Talos
  interface as with wg0. The tun comes up without lighthouse
  reachability, and the LAN-address SANs keep `talosctl`/`kubectl`
  recovery working at the DHCP address. An endpoint apply costs ~30s of
  apiserver connection-refused; it reconverges unaided.
- 2026-07-31 — anything cached that points at `10.99.0.54` (old
  kubeconfigs, talosctl contexts, muscle memory) still works while the
  dual overlay runs, and dies at step 3 when wg0 is stripped. The
  checked-out `talos/talosconfig` + `kubeconfig` are already re-pointed
  at `10.42.218.125`.
- 2026-07-30 — **the hub re-mints its own nebula leaf at every unseal**:
  the hub's leaf fingerprint rotates per deploy+unseal cycle while the
  issuer (derived CA `b881d6ff…`) stays fixed. Never pin the hub's leaf
  fingerprint anywhere — pin the CA.

- 2026-07-30 — **wg0 is fully deleted** (phase 2 step 3): anything
  referencing `10.99.0.x`, `wgup`, `wgping`, `talos.wg`, or udp/51820
  is dead — including the wg-flavoured notes above, kept struck-through
  for history. Laptop cleanup owed: remove any leftover `talos-laptop`
  wireguard interface / wgup autostart; `/tmp/wg-talos.conf` is
  obsolete (resolves the first absorbed-handover item below).
- 2026-07-30 — **first `apply` after a hub redeploy can time out**:
  the laptop→cp1 mesh tunnel needs lighthouse re-registration + a
  fresh handshake after the hub restarts. Warm it with
  `ping 10.42.218.125`, then retry — succeeded on second attempt.
- 2026-07-30 — ~~the cached `~/.config/talos-mesh/laptop.yml` predates
  phase 2: it still carries the retired wg0 underlay-filter entry and a
  hand-set `tun.dev: nebula1` the hub never rendered. Harmless (nebup
  honors the existing dev name), but `nebup -reenroll` refreshes it —
  and split-DNS answers will then appear on `nebula0`, the name nebup
  pins itself.~~ Re-enrolled 2026-07-31; split-DNS confirmed answering
  on `nebula0` (scoped service names resolve in ~19ms).
- 2026-07-30 — hub-redeploy mesh warmup reconfirmed on the plain
  deploy+unseal path (not just `apply`): cp1 unreachable over the mesh
  for ~45–60s after unseal, then recovers unaided — first pings
  relayed/lost, then 1.2ms direct. Don't page on the first failed ping
  after a deploy.
- 2026-07-30 — `MESH_CA_PIN` in fly.toml pins the derived mesh CA
  (`b881d6ff…`); a wrong-wallet unseal now fails loudly instead of
  bringing up an untrusted mesh. Re-derive offline with
  `recover -ca-fingerprint -sig <unseal-sig>`.

- 2026-07-31 — **cp1 reprovisioned onto the new disk layout**: ~~node name
  is now `talos-ezw-edv`, LAN lease `10.0.0.32`~~ — as of the second
  reprovision the same day it is **`talos-wu6-eib`, lease `10.0.0.21`**
  (Talos' generated names are not stable across reinstalls; w1 pins its
  hostname for exactly this reason). Partitions: p3 STATE
  (luks), p4 EPHEMERAL 160GiB (luks), p5 `u-media` 300GiB (xfs, plain)
  at `/var/mnt/media`.
- 2026-07-31 — **a plain `talosctl reset` wipes the whole disk,
  media library included.** The standard reinstall is now
  `reset --system-labels-to-wipe STATE --system-labels-to-wipe
  EPHEMERAL`: it *deletes* those partitions (not just contents), the
  bootloader survives, the node returns in maintenance mode on a LAN
  lease, and `apply-config --insecure` + `bootstrap` rebuilds — no USB
  medium needed. u-media is untouched and re-adopted by label.
- 2026-07-31 — the media library is **empty** post-migration (old
  `/var/media` went with the wipe, as agreed). u-media re-adoption
  across a reinstall is proven by mechanism but not yet exercised.
- 2026-07-31 — **Talos enforces the `baseline` Pod Security Standard on
  workload namespaces**: hostNetwork/hostPort pods are silently
  forbidden (DaemonSet DESIRED 1, CURRENT 0; the error only shows in
  `describe ds` events). Fix is a `pod-security.kubernetes.io/enforce:
  privileged` namespace label — ingress-nginx carries it declaratively
  via ArgoCD `managedNamespaceMetadata`. Any future hostNetwork
  workload will hit the same wall.
- 2026-07-31 — services are now `http://<name>.cp1.mesh.internal/`
  (no ports); NodePorts remain only for LAN-direct access (TV →
  jellyfin `10.0.0.x:30096`). The deployment guide still documents the
  NodePort path as primary — flagged, not yet updated.
- 2026-07-31 — deleting an ArgoCD Application without the
  resources-finalizer still pruned its helm children cleanly in
  practice (tailscale ns fully gone). Don't rely on it — but don't
  assume orphan cleanup is always owed either.

- 2026-07-31 — **ArgoCD polls git every ~3 min**; a push is not a
  deploy until then. Nudge with `kubectl annotate application apps -n
  argocd argocd.argoproj.io/refresh=normal --overwrite`. Also: the
  jellyfin pod once raced its own ConfigMap sync (deployment rolled
  before the new script landed) — if a configurator ran the old
  script, just `rollout restart`.
- 2026-07-31 — **siwe-oidc restart semantics differ per relying
  party**: a bridge pod restart rotates the JWKS → ArgoCD sessions die
  (it re-validates tokens) but oauth2-proxy sessions survive (own
  cookie, sealed secret). Not a bug; know which one you're debugging.
- 2026-07-31 — the siwe-oidc image deploys as `:latest` +
  `imagePullPolicy: Always`: CI push alone changes nothing running —
  `kubectl -n sso rollout restart deployment siwe-oidc` after the
  workflow finishes is the actual deploy step.
- 2026-07-31 — ghcr package `siwe-oidc` came up **public** on first
  push (no manual visibility flip was needed).

- 2026-07-31 — **a machine waiting on device-flow approval has no apid**:
  port 50000 is *refused*, so `talosctl get disks` cannot answer and the
  hardware is un-inspectable until it is declared, approved and
  installed. Chicken-and-egg for `install.disk`. The way out: the hub
  logs the machine's full identity at `/device/code` time, so
  `fly logs | grep "device auth started"` yields mac + **uuid** +
  serial with no wallet and no approval — enough to write `meta.yaml`,
  including the uuid that makes the KMS allowlist durable from the
  first boot.
- 2026-07-31 — **`install.disk: /dev/sda` in `base/worker.yaml` is a
  landmine**: w1 (Alienware x15) boots from a 2.1GB USB stick that
  presents as `sda`, so the role default would have installed Talos onto
  the boot medium. Removing the key needs an RFC6902 patch — a merge
  patch cannot delete, and `disk: ""` is silently dropped, which leaves
  *both* `disk` and `diskSelector` set with an implicit precedence rule
  deciding the winner.
- 2026-07-31 — **a worker cannot reuse the cluster layer.**
  `base/worker.yaml` + `clusters/homelab/{cluster,secrets}.yaml` fails
  validation ("etcd config is only allowed on control plane machines"
  plus three CA-key rejections). Workers take
  `worker-cluster.yaml` + `worker-secrets.yaml`; regenerate the latter
  with the `yq del(...)` one-liner in its header whenever `secrets.yaml`
  changes.
- 2026-07-31 — `talosctl` global flags are **not** global: `-n`/`-e` must
  follow the subcommand (`talosctl version --insecure -n IP`, not
  `talosctl -n IP version`), and maintenance-mode reads are `get -i`,
  not `--insecure`.
- 2026-07-31 — **`~/.kube/config` is dead**: it still routes through
  `zitadel.marnyg.xyz` + `kube-oidc-proxy`, both removed in the SSO arc.
  `kubectl` only works with `--kubeconfig ./kubeconfig` from the repo
  root until it is replaced.
- 2026-07-31 — **the media library is empty and declared disposable**,
  which is the only reason the missing `nodeAffinity` on the media
  hostPath PVs is not yet an incident (bug 0b374653): w1 is schedulable,
  and a media pod landing there gets an empty `DirectoryOrCreate` mount.
  Must be fixed or superseded by Longhorn **before** the library is
  refilled.

- 2026-07-31 — ~~the media library is empty and declared disposable,
  which is the only reason the missing `nodeAffinity` ... is not yet an
  incident (bug 0b374653)~~ Superseded same day: hostPath is gone, media
  is on Longhorn RWX. The bug's premise was also **wrong in our favour**
  — a media pod on a node without the volume fails loudly (`mkdir
  /var/mnt/media: read-only file system`, because `/var/mnt` is
  read-only on Talos unless a user volume mounts there), it does not
  silently mount an empty `DirectoryOrCreate`.
- 2026-07-31 — **Nothing on this cluster is replicated.** Longhorn is
  installed and healthy on 1073GB, but the 2-replica StorageClass has no
  users: every app keeps config on `emptyDir`. App state dies on pod
  restart, not just reinstall. Do not read "Longhorn is up" as "state is
  safe".
- 2026-07-31 — **Knowing deviation from invariant 2** (decision
  `d5f73e89`): `longhorn-bulk` runs 1 replica, so the media library is
  neither git-derivable nor replicated — wiping the node holding it
  destroys it. Accepted only until the new nodes land (`da61bd8e`).
  This is the wrong implementation, not a relaxed invariant.
- 2026-07-31 — **Anything that dials a `.mesh.internal` name from a pod
  only works if that pod lands on cp1.** nebula routes 10.42.0.0/16
  source addresses; a pod's source is 10.244.x.x. Bit oauth2-proxy this
  session (~70min SSO outage) and ~~`argocd-server` is still broken the
  same way (`f9bac57c`)~~ fixed 2026-08-05 (`fed04b4`), along with a
  third instance in jellyfin (`f1f5dd4`). Rule of thumb: pods talk to
  Services, the mesh is for hosts and browsers.
- 2026-07-31 — `talosctl wipe disk <part>` does **not** free a user
  volume's space despite the docs saying it makes the disk allocatable.
  It clears the filesystem, leaves the partition entry, and the
  replacement volume sits in `failed` / "not enough space".
  `--drop-partition` is the real flag. Partitions renumber (`p5` → `p4`).
- 2026-07-31 — cp1's DHCP lease moved four times in one day
  (`.33 → .21 → .35 → .41`). Never hardcode it; use the mesh address
  `10.42.218.125`.

- 2026-08-05 — **w1 is down** (since 2026-08-04 ~09:26: no LAN ping, no
  apid, kubelet silent). Media library offline — all three `longhorn-bulk`
  volumes `faulted`, single replica on w1. Needs physical power/console
  attention; data presumed intact on disk. Until it returns, do not start
  storage work: the 2-replica class can't place healthy volumes on one node.
- 2026-08-05 — **patching upstream-owned resources from git**: the pattern
  is an SSA partial manifest with annotation
  `argocd.argoproj.io/sync-options: ServerSideApply=true,Validate=false`
  (first use: `k8s/apps/argocd/server-patch.yaml`). ArgoCD owns only the
  declared fields. Watch: a *previously* patched field owned by another
  field manager (e.g. the old installer-Job `kubectl patch`) is NOT removed
  by SSA merge on keyed lists — the stale argocd-server alias was removed
  with a one-time live `--type=json` replace before the SSA manifest synced.
- 2026-08-05 — jellyfin's old pod (pre-`f1f5dd4`) still runs with
  admin/admin until it cycles; the new pod is blocked on the faulted media
  volumes. Retrieve the new password after w1 returns:
  `kubectl -n media get secret jellyfin-admin -o jsonpath='{.data.password}' | base64 -d`.

- 2026-08-14 — **win2k25 reinstall is one API act**:
  `kubectl -n vms create job --from=cronjob/win2k25-reinstall
  win2k25-reinstall-$(date +%s)` (wrapped by
  `scripts/win2k25-reinstall.sh`); RDP is back ~25 min later. Password:
  `kubectl -n vms get secret win2k25-admin -o
  jsonpath='{.data.password}' | base64 -d`. Rotating it requires
  resealing BOTH win2k25-admin and the autounattend secret.
- 2026-08-14 — **ArgoCD-managed Jobs**: never set
  `ttlSecondsAfterFinished` (ArgoCD prune-recreates the Job forever
  once the TTL reaps it); Jobs are immutable, so script changes mean
  bumping the Job name (`-v2`, …). Settings ConfigMap changes
  (`resource.customizations.*`) are picked up live; `cmd-params-cm`
  changes are not.
- 2026-08-14 — **`virtctl stop/start` is futile under selfHeal**: the
  next sync reverts it. VM start/stop is a git edit of `runStrategy`
  (Always ↔ Halted).
- 2026-08-14 — the win2k25 system/ISO Longhorn volumes run `degraded`
  (1 of 2 replicas) while w1 is down; they heal unaided when it
  returns. Distinct from the `faulted` media volumes, which have no
  live replica at all.

- 2026-08-14 — **ADR-0012 is live: enrollment mints only device-born
  keys.** Old master-derived device certs (office MacBook enrolled as
  `laptop`, the phone) remain *valid* until their 90-day expiry — the
  CA did not change. Re-enrolling under the same name keeps the same
  address, so the office-Mac/home-laptop name collision (2026-07-30
  note) persists at the address level; blocklist the old fingerprint
  if simultaneous use ever matters.
- 2026-08-14 — **Stock Mobile Nebula accepts the hub-issued yaml**
  (owner-verified) — updates the 2026-07-30 hand-entry note: phone
  onboarding is "move the yaml + key onto the phone", not manual
  field entry. Renewal is a re-import every 90 days (`5183f6ea`).

- 2026-08-15 — **The Android app is distributed via the rolling
  `android-latest` release**: every push touching `android/` or
  `config-server/{mobile,devkey}` re-clobbers
  `releases/download/android-latest/talos-mesh.apk`. "Update the app"
  = re-download that URL on the TV (debug-signed, so versions install
  over each other). CI is the only builder — there is no local
  Android SDK on the dev machine.
- 2026-08-15 — **Hub deployed with `/hosts` + the media tcp/80
  firewall rule** (owner deploy + unseal same day). A TV enrolled
  before this deploy would hold a valid cert but get firewall-dropped
  on the device list — not an issue for devices enrolled after.
- 2026-08-15 — The app pushes **no DNS server to the VpnService**
  (deliberate — Android sends *all* device DNS to a VPN resolver and
  the hub only answers the mesh zone). Symptom to expect: mesh *names*
  don't resolve on the TV; the app's host list carries name + IP, and
  services are reached by IP.

- 2026-08-16 — **`pi -p` hangs after completing its answer** when
  stdin is a TTY/pipe left open: it produces output, then never exits.
  Wrap non-interactive uses with `</dev/null` and a `timeout`:
  `timeout -k 10 420 pi -p --no-session "…" @file </dev/null`. Used
  for the adversarial doc-review loop; will bite any scripted use.

- 2026-08-16 — **A `/policy` overlay does not change the hub's own
  running firewall** — the hub scope renders at unseal, which precedes
  any overlay in the process's lifetime. Overlay experiments are
  visible in *device* configs (next enrollment) and *node* configs
  (next `apply`) only; don't misread the hub scope as a no-op bug.
  Also: every deploy drops the overlay — export before deploying if it
  should survive.

### Absorbed from the legacy `handover.md` (2026-07-24), still open

These were unchecked loose ends when that file was written; none have
been verified since, so treat them as unconfirmed. Not filed as tasks —
ask before doing so.

- Laptop WG config lives in `/tmp/wg-talos.conf` — move somewhere
  permanent + `chmod 600` (user action).
- ~~`argocd-dex-server` pod in Error — unused OIDC component; candidate
  for disabling in the ArgoCD install.~~ Scoped 2026-07-31: dex gets
  deleted as part of the ArgoCD→SIWE-OIDC switch (`01199df7`).
- ~~`tailscale-operator` Application Degraded — never investigated.~~
  Moot 2026-07-31: tailscale removed entirely (ADR-0009).
- KMS slot 0 is never used at boot (early-boot DNS loses the race).
  Accepted; a KMS endpoint by IP instead of hostname would dodge DNS.
- Old kubeconfigs pointing at `10.0.0.x` are dead — regenerate.
