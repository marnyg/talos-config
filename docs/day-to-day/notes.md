# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review).

     Pruned 2026-09-03: wg0-era and nebula-phase-1/2 history removed
     (struck-through entries live in git history before this date).
     Pruned 2026-09-16: Phase-0-gate-era cautions, stock Mobile Nebula,
     zitadel-era kubeconfig reason, duplicated gotchas.
     Pruned 2026-10-24: 73 entries ≤ 2026-09-22 (mesh v3 P2–P4
     bring-up, phone/TV dev loop, hub ops, hardware quirks) — the
     durable ones promoted to technical/guides/gotchas.md, the rest
     superseded; full text in git history before this date.
     Pruned 2026-09-30: nebula-era facts (mesh/, meshIP, v2 recipe/blocklist,
     leaf re-mint, spike relay app), superseded milestones (ADR-0001/0018
     "not built", v2 enrollment, poisoned seq, irohup stranding), entries
     contradicted by later ones (DHCP cp1, w1 back, CI-built APK), and
     duplicate vendorHash/tagged-test notes (AGENTS.md carries those).
     Reference beads issues by id (`talos-config-xxx`). -->

## Read first

- 2026-10-20 — **Review bounces are bounded** (`6bvk`, `b8bc661`): an
  agent sending a task back along the same edge `Retries` times parks
  it at `needs-human` ("sent reviewing → executing by an agent for the
  3rd time"). A person's `sap move` is not counted, so re-sending after
  a park gives the agents one more bounce before it parks again. To
  stop a live run by hand: `sap abort <live session>`.
- 2026-10-19 — **win2k25 is pinned to w2 by its GPU** (`hostDevices`):
  it cannot schedule on cp1/w1/nas1 any more, and a w2 shutdown takes
  the VM down with it (no live migration — it never was migratable).
  The node advertises `nvidia.com/GA104_GEFORCE_RTX_3070: 1` only
  while vfio-pci holds the card; nothing on the host can use the GPU
  meanwhile (no nvidia extension on the fleet image).
- 2026-10-19 — **w2 was powered off and moved to another room**
  (`talosctl shutdown` ~20:40Z, back ~20:42Z) and **rejoined
  unattended** — static address, UUID-keyed unseal, p0agent beat —
  the first cold restart of a node with the hub unsealed and nothing
  touched. That is the expected path for any node now. Do NOT plug the Realtek USB FE dongle
  (`32:0b:b1:…`) back in: a driver-generated MAC enumerating first
  would fetch a config that does not exist. Longhorn has no replicas
  on w2 yet, so nothing degrades while it is away.
- 2026-10-16 — **HTTPS on the mesh is device-local (ADR-0033).** Each
  daemon's root is `<state>/tls/ca.crt` (Mac:
  `/var/lib/talos-mesh/marius-mac.iroh/tls/`, key 0600 beside the
  member key). Trusting it is per device, once: Mac — `sudo security
  add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain
  <ca.crt>` by hand (launchd's attempt is refused: no user session),
  then **restart Firefox** (it reads the keychain only at startup);
  Android — app → "Install HTTPS certificate" → Save → Open Settings →
  *More security settings* (Sony) → Encryption & credentials → Install
  a certificate → CA certificate (PIN). Deleting `tls/` re-mints a new
  root: every browser on that device re-trusts. Phone root:
  `talos-mesh device CA 097e77d2 (phone)`; Mac: `af40c8b2 (marius-mac)`.
- 2026-10-16 — **Driving the Sony over USB:** no `adb` on PATH; `nix
  build nixpkgs#android-tools`. Debug APK ⇒ `adb shell run-as
  dev.marnyg.mesh` reads the app's files (`files/member/…`); Chrome
  DevTools via `adb forward tcp:9222
  localabstract:chrome_devtools_remote` (`/json/list` shows service
  workers; CDP `Page.getInstallabilityErrors`). Secure windows (PIN,
  biometric) screenshot black and need the human.
- 2026-10-16 — **Mac rebuild is `sudo darwin-rebuild switch --flake
  .#mac`** in `~/git/nixos` (not `nix run .# -- switch`); it needs the
  password, so pre-build with `nix build
  .#darwinConfigurations.mac.system` and hand the switch over.
  Headless `nvim` checks lie: `image.nvim` aborts `init.lua` without a
  terminal, so filetypes/LSP after it never load — test in a pty.

- 2026-10-16 — **workouts is live** at `workouts.gw.mesh.internal`,
  seeded. The image is scratch (no `sh`/`tar`), so `kubectl cp` and
  `exec` do not work on it: to re-seed, use a busybox pod on the PVC
  for the copy and a one-off pod from the app image for `workouts
  import` (the deployment.yaml header is wrong on this; fix pending).
  A copy of the export sits on the volume at
  `/var/lib/workouts/strong-export`; the original is
  `~/strong-export-2026-10-16/` on the laptop only — licensed media,
  never in git (`yz75`).

- 2026-10-04 — **Commits on `main` must be ssh-signed by the owner key**
  (ADR-0030, `lehx`): the pre-push hook refuses an unsigned tip, and once
  deployed the hub will not serve one. The nixos box is configured; the
  darwin laptop is NOT yet (four `git config` lines in AGENTS.md). No
  GitHub-UI merges. `SKIP_SIGNED=1` exists for emergencies — an unsigned
  tip on `main` then means the hub stays on the previous tree until a
  signed commit lands on top.
- 2026-10-04 — **ADR-0030 is live** (hub 3896837): `talos/` edits are
  served within 3 min of a signed push, or seconds after
  `curl -X POST https://marnyg-talos-config.fly.dev/git/nudge`
  (proven 2026-10-05: 6 s). The "talos/ from git" row on `/status`
  says which tip; `fly logs` shows `git: serving <ref>@<sha>` after
  each swap. A deploy is only for hub code now.
## Workloads / storage

- **Knowing deviation from invariant 2**: `longhorn-bulk` runs 1
  replica — the media library is neither git-derivable nor
  replicated. Wrong implementation, not a relaxed invariant.
  **`talos-config-0q0` is NOT unblocked by w1's return** (asserted in
  error 2026-09-19, corrected same day): w1 was always one of the two
  nodes in the storage class's "~1073GB raw across two nodes", so
  coming back from an outage adds no capacity. The bulk PVCs are
  ~450G provisioned on w1 and cp1 has ~256G free; replica anti-affinity
  puts the second copy on the *other* node, so replicas: 2 cannot
  schedule. "Once the new nodes land" means new hardware with disk —
  and ADR-0011 notes Longhorn's own minimum is **3 nodes**.
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
  VM power is live state since 2026-10-22 (decision `4440`):
  `/spec/runStrategy` is in `ignoreDifferences` **and** the children
  sync with `RespectIgnoreDifferences=true` — both are needed, the
  first only hides the diff and a sync still writes git's value back.
  `vmpower` patches it; `virtctl stop/start` would now stick too.
- siwe-oidc: a bridge restart rotates the JWKS → ArgoCD sessions die;
  the group gate (`/authz`, ADR-0032) has no session to lose. The image
  is `:latest` + `imagePullPolicy: Always` — CI push changes nothing
  until `kubectl -n sso rollout restart deployment siwe-oidc` (or an
  args change rolls it).
- 2026-10-06 — **Gated apps all 401 ⇒ check the gateway pin first.**
  `/authz` trusts only the `-gateway=ed:…` ids in
  `k8s/apps/siwe-oidc/deployment.yaml`; a gateway on a fresh volume is
  a new id (its log: `identity token issuer ed:…`). Also 401 if the
  gateway image predates `31f4f24` (no token minted) — the gate fails
  closed, never open. `kubectl -n sso logs deploy/siwe-oidc | grep
  authz:` names the reason per refusal.
- 2026-10-06 — Deploy order for bridge+gateway changes: push Go first
  (CI builds the bridge image in ~40 s; the gateway image is built on
  the box, `HUB_BUILDER=mar@nixos k8s/apps/gateway/build.sh`, ~5 min,
  and pinned by digest), manifests second. A manifest that adds a
  bridge flag before its image exists CrashLoops until the next pull.
  Build the gateway image from a clean tree or the tag is `-dirty`
  (same digest; the pin is cosmetic but ugly).
- win2k25 reinstall is one API act: `scripts/win2k25-reinstall.sh`;
  RDP back ~25 min later, at `xfreerdp /v:rdp.gw.mesh.internal`
  (gateway `rdp` facet, admins; the old NodePort 30389 is gone —
  nothing on mesh v3 could reach it). Fallback: `kubectl -n vms
  port-forward svc/win2k25-rdp 3389:3389`. Password in secret
  `win2k25-admin`; rotating
  it requires resealing both that secret and the autounattend secret.
- jellyfin's admin password: `kubectl -n media get secret
  jellyfin-admin -o jsonpath='{.data.password}' | base64 -d` (the new
  pod is blocked on the faulted media volumes until w1 returns).

## CI / orchestration

- 2026-10-06 — **A `ReverseProxy` hook that reads a body must own
  `Accept-Encoding`**: forward the client's `gzip` and the upstream
  compresses; `ModifyResponse` then sees bytes (`resp.Uncompressed`
  false). Delete the header on that request and Go's transport
  negotiates gzip itself and decodes transparently. Found by the TV
  (OkHttp sends gzip; `nc` and curl do not, so the laptop test and
  the unit tests passed).
- 2026-09-29 — **A dead node freezes GitOps**: StatefulSet pods on a
  NotReady node stay `Terminating` forever and never reschedule.
  `argocd-application-controller-0` sat on w1 from 09-21 to 09-29, so
  ArgoCD reconciled nothing for 8 days (stuck at `22a84de`) and
  nothing said so. Symptom: a push that never deploys. Fix:
  `kubectl delete pod -n argocd argocd-application-controller-0
  --force --grace-period=0`. Structural fix is part of the HA sweep
  (`9l67`, noted there).
- 2026-09-29 — **`/var/run/docker.sock` on the Mac is podman's**
  (a symlink into `~/.local/share/containers/podman/`), not Docker
  Desktop's. Anything defaulting to it talks to podman. The actors
  docker driver now resolves like the docker CLI (DOCKER_HOST → docker
  context → socket), so it follows `docker context show`; other tools
  may not.
- 2026-09-29 — **The sap-actors image has no shell or `cat`.** No
  need to read files off its pods any more: every actor binary takes
  `-state DIR -print-id`, and the provisioner is found by `#lookup` at
  `sap-lighthouse` (`0bc.6`). If a file must be read anyway:
  `kubectl debug --target=<container> --image=busybox --profile=restricted`
  and `/proc/1/root/…` (ephemeral containers stay until the pod
  restarts). The `sap` namespace is PodSecurity `restricted`: without
  `--profile=restricted` the debug container is refused (the default
  profile adds `SYS_PTRACE`); same-uid `/proc/<pid>/root` reads need no
  ptrace. Use `KUBECONFIG=./kubeconfig` from the repo root.
- 2026-09-29 — **A node that is truly off: taint it out of service.**
  `kubectl taint node w1 node.kubernetes.io/out-of-service=nodeshutdown:NoExecute`
  made KCM force-delete every ghost pod on w1 and release its three
  `VolumeAttachment`s within 30 s; the gateway attached on cp1, the
  win2k25 VM rescheduled, the hung ArgoCD op completed by itself.
  Precondition: the node is powered off, not merely unreachable — the
  taint asserts nothing runs there. **Before w1 rejoins, remove it**
  (`kubectl taint node w1 node.kubernetes.io/out-of-service-`); a
  kubelet does not tolerate it. Structural successors: Longhorn
  `nodeDownPodDeletionPolicy` (volumes) and the argocd controller pin
  (`k8s/apps/argocd/controller-patch.yaml`), both from `9l67` slice 1.
- 2026-09-29 — **A member that missed > 7 d of beats before `6ccabed`
  is stranded** — `renew: … effective chain is expired (beating with
  the held certs)` in its log forever, its names gone from the map,
  though the pod/agent looks healthy (`5hek`). The gateway was; **w1
  will be when it comes back** (off since 09-21). Recovery is one
  wallet act each: gateway — delete `kit.json` in `gateway-state` (or
  the PVC) and sign the URL from `kubectl logs -n gateway
  deploy/gateway`; w1 — re-serve its config (device-code recipe,
  2026-09-19 note) and `apply-config`. Agents built after `6ccabed`
  print that themselves instead of retrying; kits minted or renewed
  after the fix carry 90 d beat grants and never hit it inside 45 d.
- 2026-09-29 — **The `wg0` assertion in `TestConfigRefusedWhileSealed`
  is a flake**: the served config embeds base64 cert material, and a
  random signature can contain the substring `wg0`. Seen once in a
  full tagged run, passes alone. Fix is a real assertion (bead).
  _Fixed 2026-10-17 (`d4p8`): the check is a YAML-line regexp now._
- 2026-09-29 — **An ArgoCD sync op can sit "Running" for hours** on
  "waiting for healthy state of <wave-0 resource>" when any later
  wave exists (`vms/` uses waves 1–2): the gateway's ghost pod on w1
  held the `676a791` op open from 10:35Z, and `apps` showed *Synced*
  because live state matched — no newer revision was ever applied.
  `kubectl -n argocd patch app apps --type merge -p
  '{"status":{"operationState":{"phase":"Terminating"}}}'` ends it;
  auto-sync starts a fresh op that applies wave 0 and hangs again.
  Check `.status.operationState.{phase,startedAt,message}`, not the
  sync status, when a push seems not to land.
- 2026-09-30 — **Required pod anti-affinity + replicas == schedulable
  nodes deadlocks a default rolling update**: the surge pod has no
  node, and `maxUnavailable: 25%` rounds to 0 so no old pod is ever
  retired — the new pod sits `Pending` forever and the Deployment is
  `Progressing` forever (no `progressDeadlineSeconds`, so never
  Degraded). Hit on the slice-2 rollout with only cp1 + nas1
  schedulable. Any anti-affine Deployment needs `maxUnavailable: 1`
  (ingress-nginx via `controller.updateStrategy`; oauth2-proxy had it
  via `strategy` until it retired, ADR-0032). **Unsticking the ArgoCD op it wedged** (both the
  parent `apps` and the chart app `ingress-nginx`): `kubectl patch
  app <name> -n argocd --type json -p
  '[{"op":"remove","path":"/operation"}]'` — then the controller
  may not notice for minutes; `kubectl annotate app <name> -n argocd
  argocd.argoproj.io/refresh=normal --overwrite` makes it reconcile
  at once, and auto-sync picks up the newest revision. Watch
  `.status.operationState.startedAt` change; a Running op whose
  `startedAt` predates your push applied the *old* spec.
- 2026-10-01 — **Longhorn `nodeDownPodDeletionPolicy=delete-both` only
  force-deletes pods that are already `Terminating`** — it does not
  evict. A Deployment pod with a Longhorn RWO volume on an unreachable
  node still waits out its `node.kubernetes.io/unreachable` toleration
  (300 s default) before Longhorn touches it; slice 1 saw 30 s only
  because the out-of-service taint evicts at once. Any single-replica
  pod on a Longhorn volume that should fail over in ~1 min needs the
  30 s `unreachable`/`not-ready` tolerations (gateway, siwe-oidc have
  them). cp1 is the only control plane, so pods pinned there have no
  node-loss story to tell — only nas1/w1 residents matter.
- 2026-10-03 — **Admin from `mar@nixos` is on the identity plane now**:
  `talos-mesh.service` (`irohup -tun`, user `talosmesh`, state
  `/var/lib/talos-mesh/nixos.iroh`, log `journalctl -u talos-mesh`),
  tun `talosmesh0`, zone `~mesh.internal → 198.18.0.2` declared
  per-link in systemd-resolved (`resolvectl status talosmesh0`;
  `resolvectl query cp1.mesh.internal` is the probe — plain `dig`
  bypasses resolved's routing). Re-enroll: `talos-mesh-enroll
  -reenroll`. The unit is gated on `kit.json`: after the first enroll,
  `sudo systemctl start talos-mesh` once. Kubeconfig here still points
  at the dead nebula IP — pass `--server https://cp1.mesh.internal:6443`
  or run `nix run .#kubeconfig`.
- 2026-10-03 — **When the mesh is down, LAN-direct still works from this
  box** (invariant 4 exercised): `talosctl -e 10.0.0.68 -n 10.0.0.68`
  with `talos/talosconfig`; apid has request forwarding off, so each
  node must be dialled at its own LAN IP (nas1 `10.0.0.74`, w1
  `10.0.0.71`). cp1's console `address-overlap` diagnostic appeared
  while its link was down and cleared with the replug.
- 2026-10-03 — **A node that was off longer than its speak-as comes back
  looping on `effective chain is expired`** (agent fix `7fb6473`; on the
  whole fleet as p0agent 0.1.6 since 2026-10-04, `9af0` — the workaround
  below is history). Workaround that worked on w1: re-serve
  (fresh 1 h boot token), `kubectl -n kube-system debug node/<n>
  --image=busybox:1.36 --profile=sysadmin --attach -q -- rm
  /host/var/lib/p0agent/{kit,bundle,hub}.json` (keep `key`), then
  `talosctl service ext-p0agent restart` — enrolled in <1 s, same NodeId.
- 2026-10-03 — **`nix run .#apply` dry-runs first** and refuses (a) any
  diff touching `systemDiskEncryption` unless `APPLY_REKEY=1` (since
  2026-10-04; it meant "no override, fix `meta.yaml` installMAC" for a
  day) and (b) a reboot unless `APPLY_REBOOT=1`. Verify a
  node's on-disk slot-1 passphrase against the hub's compose without
  printing either: compare the `passphrase:` prefixes of `talosctl get
  mc -o yaml` and `curl http://hub.mesh.internal/config?mac=…`.
- 2026-10-03 — **The hub test suite is timing-sensitive under build
  load**: `nix build .#config-server-bin` compiles every `cmd/` while
  packages' tests run; a test that passes under `scripts/test-iroh.sh`
  can fail in the sandbox (TestNodeAgentEndToEnd did, deterministically,
  until the sleeper rewrite). Compare both before blaming a change.
- 2026-10-04 — **Longhorn reads `node.longhorn.io/default-disks-config`
  only for a node it has no disks for.** Adding a disk to a registered
  node (nas1's bays) means patching `nodes.longhorn.io/<node>`
  `spec.disks` by hand. The annotation in the machine patch is what a
  reinstall reproduces.
- 2026-10-04 — **Recreating a StorageClass under ArgoCD races
  self-heal**: deleting it before ArgoCD's target revision is the new
  commit gets the *old* class re-applied, and the next sync fails with
  "updates to parameters are forbidden". Push, wait until `apps` shows
  the new revision, then `kubectl delete sc`.
- 2026-10-04 — **Deleting an RWX share-manager pod restarts every
  Deployment pod mounting that volume** (`rwx-volume-fast-failover`
  off); bare Pods keep their mount and keep working. The pod lands
  where the class's `shareManagerNodeSelector` says. Patching
  `sharemanager.status.ownerID` does not move it (the owning manager
  re-asserts on the pod-delete event).
- 2026-10-04 — **The old docker host still holds the library**:
  `mar@nixos:~/disks/1TB-old/server/{tv,movies}` (466 GB, imported into
  the cluster today) with its media containers **stopped, not
  removed**, and Sonarr's export at `sonarr-series-2026-10-04.json`
  beside them. It is the only second copy until the owner retires it.
- 2026-10-03 — **`irohtransport.Raw.Close` does not interrupt a Read
  blocked in the FFI.** iroh-ffi serialises `read()`/`stop()` on one
  tokio lock and the bindgen has no cancel, so any splice over a `Raw`
  (gateway WebSockets, `meshtun.Pipe`, nodeagent `splice`) only returns
  when the peer ends its side or the connection dies — `Close` resets
  our send side to provoke that. Do not add a "Stop on Close" to
  unblock it; that is the deadlock `vzbf` found. Thread `vh6e`.
- 2026-10-03 — **Media app state is on PVCs now; a media pod restart
  is a ~30 s outage, not a wipe.** sonarr/radarr/jellyfin/transmission
  run `strategy: Recreate` on RWO `<app>-config` volumes, so
  `kubectl rollout restart` takes the app down until the new pod
  attaches (longer if it lands on another node). jackett and nzbget
  are still `emptyDir` by design — their `/config` is templated on
  every start.
- 2026-10-03 — **The docker host's sonarr/radarr DBs are now older
  than the cluster's.** They were imported into the new PVCs that
  evening (`vu9n`); any further change happens in the cluster. Do not
  re-import. Jellyfin was *not* imported (owner's call).
- 2026-10-03 — **ArgoCD auto-sync stalls behind an unhealthy
  Deployment.** The `apps` sync waits for each Deployment to go
  healthy; a crash-looping pod from one commit holds the *next*
  commit's fix in OutOfSync until the operation times out. `kubectl
  apply -f` the committed manifest to unblock; identical content, so
  ArgoCD reports Synced once the pod is healthy.
- 2026-10-03 — **Jellyfin is the upstream `jellyfin/jellyfin` image,
  not linuxserver's**, pinned by dated tag; its four `JELLYFIN_*_DIR`
  envs keep lsio's `/config` layout. Bumps: `skopeo list-tags` for the
  `12.x.YYYYMMDD-HHMMSS` tag. The lsio build cannot run non-root.
- 2026-10-04 — **The `/status` `storage` row is silent when healthy.**
  It logs `storage: …` only when the warn line *changes*, so a fresh
  hub's first good poll leaves nothing in `fly logs`; the page is the
  check. `degraded` during a Longhorn rebuild (disk swap, node return)
  is expected and clears on its own; `FAULTED` is not.
- 2026-10-04 — **`files/samba` is a LAN-open SMB server on nas1:445**
  (password auth, not wallet). Temporary for the Windows PC transfer;
  set `replicas: 0` in `k8s/apps/files/deployment.yaml` when done.
  `longhorn-bulk` was recreated this day with corrected
  anti-affinity values — the first PVC ever provisioned through it is
  `files/transfer`; media volumes predate the fix and were hand-patched.
- 2026-10-04 — **Rebooting nas1 rebuilds the whole bulk tier** (`k8sd`):
  both replicas of every `longhorn-bulk` volume are on nas1, so a reboot
  fails both and Longhorn copies the live head from the salvaged one —
  ~600 GB at ~12 MB/s, i.e. hours, and the three bulk rebuilds pin the
  node at `concurrent-replica-rebuild-per-node-limit` (5) so small
  nvme-tier rebuilds *onto* nas1 time out (`failed to start rebuild …
  context deadline exceeded`). Deleting the stuck replica CR made
  Longhorn reuse the failed one on the other node within a minute. The
  Talos drain always times out at 5 min on nas1 (bulk PDBs,
  `block-if-contains-last-replica`) and the upgrade proceeds anyway —
  that is Talos' `DrainTimeout`, not a hang.
- 2026-10-04 — **A slot-1 (break-glass) passphrase change is two boots
  with the hub unsealed**, not one: boot A opens STATE on the old slot
  1 (META still holds the old block), writes the new block to META and
  re-keys EPHEMERAL; boot B rejects slot 1 on STATE, opens it via slot
  0 (KMS) ~30 s later and re-keys. Check `kmsprobe -uuid <node uuid>`
  says `roundtrip OK` before each boot; do not deploy the hub (it
  re-seals) between A and B. `talosctl get volumestatus STATE` shows
  `encryptionSlot: 0` once the KMS opened it.
- 2026-10-04 — **`talosctl` hangs while cp1 reboots**: the talosconfig
  endpoint is `cp1.mesh.internal`, so every `-n` goes through the node
  being rebooted. Use `-e 10.0.0.68` (cp1's LAN address) for anything
  that must run across a cp1 reboot; `kubectl` likewise times out once
  on a stale h2 connection to `cp1.mesh.internal:6443` after the node
  returns, then recovers.
- 2026-10-04 — **The EPHEMERAL slot-0 call can fail `network is
  unreachable` seconds after STATE was opened over the network** (nas1
  13:56:30, 12 s after a KMS open) — the per-handler DNS path, not
  DHCP. Harmless (slot 1 opens it, slot 0 re-syncs) but it means the
  "race" is per call, not per boot.
- 2026-10-04 — **Driving the phone over adb from the nixos box**: `adb`
  is not on PATH, only inside `android/shell.nix` (`nix-shell --impure
  shell.nix --run 'adb …'`). First connect shows `unauthorized` until the
  phone's USB-debugging prompt is accepted. `adb install -r` upgrades in
  place (same debug key, no re-enroll) but kills the VpnService, which
  only autostarts at boot — someone must tap Connect. The Go log is
  `run-as dev.marnyg.mesh cat cache/mesh.log` (phone clock logs UTC);
  `adb shell svc wifi disable|enable` is a clean handover test, and
  `adb exec-out screencap -p` + reading the PNG gets at the Debug view's
  `StatusJSON`. The hub's `/status` member table (reach-me-at column)
  needs a wallet sign-in, so the phone's own Debug view is the quicker
  endpoints check.
- 2026-10-04 — **Driving the TV over network adb from the nixos box**:
  `adb connect 10.0.0.2:5555` sits `unauthorized` until the Shield's
  on-screen prompt is accepted once per builder key. Replacing the app
  *while its tunnel is up* makes the Shield answer a blank `failed to
  install:` and drop adb offline for a few seconds although the install
  landed — check `dumpsys package dev.marnyg.mesh | grep
  lastUpdateTime` before retrying; the cold (first) install reports
  `Success`. Launching the app afterwards (`monkey -p dev.marnyg.mesh
  …`) brought the tunnel up without a Connect tap. Mixing the SDK's
  `adb` (shell.nix) with `nixpkgs#android-tools` restarts the adb
  server and re-prompts every device — pick one.
- 2026-10-04 — **Hub deploy from this box is ~3 min**: `fly/deploy.sh`
  with no `HUB_BUILDER` builds the x86_64 image locally (the tagged
  test phase runs inside), pushes, deploys; the hub comes up sealed and
  the TV/phone retry the beat every minute until the owner signs at
  `/status` (TV renewed its member cert against the new hubkey within
  60 s today).
- 2026-10-04 — **Reading a Longhorn rebuild on nas1**: a same-node
  replica rebuild (bay → bay) is a *local* sparse sync
  (`SyncLocalFile` in the instance-manager log) and the engine's
  `rebuildStatus` stays at `progress: 0, appliedRebuildingMBps: 0` for
  its whole duration — watch the target file's `stat -c %b` inside the
  instance-manager pod instead. A **snapshot purge** (`purgeStatus` on
  the engine; `snapshots.longhorn.io … markRemoved: true`) reads and
  writes the same spindle and starves any rebuild sourcing from it.
  `talosctl cgroups --preset io` attributes the IO per pod; the SATA
  letters are **not stable across reboots** (bays were `sdd`/`sde`,
  after the 13:55Z reboot `sda`/`sdb`; `sdc…sdk` are Longhorn's iSCSI
  volumes) — map with `talosctl mounts | grep longhorn` first. A
  reboot mid-rebuild throws the partial replica away (101 GB today).
- 2026-10-04 — **Phone Connect from the box**: after `adb install -r`
  the phone's app does not bring the tunnel up on launch (the Shield
  does); `adb shell input tap 178 438` on the Connect button of the
  not-connected screen (1080×2520) did, no VPN consent re-prompt.
- 2026-10-05 — **Watching a Talos node go down from here**: `talosctl
  reboot`/`shutdown` block with `--wait` (default) until the node is
  back — run them in the background or with `--wait=false` when a tool
  call must return in <60 s. `talosctl dmesg --follow` dies the moment
  apid stops (mid `stopAllPods`), and the kernel ring buffer is gone
  after the reboot, so the `unmountPodMounts` phase timing is **not
  observable remotely** — judge it by wall clock (request → kernel
  `Linux version` line; 2.5 min on nas1 is healthy). Longhorn marks a
  volume `degraded` for ~30 s while a node's replica comes back; that
  is not a rebuild (check `replicas.longhorn.io … failedAt` is empty).
- 2026-10-06 — **herdr CLI/server protocol mismatch after a profile
  update**: the nix profile's `herdr` (0.9.1, protocol 22) refuses every
  control command against the still-running 0.8.2 server (protocol 20)
  with `protocol_mismatch`; the suggested `herdr server stop` kills every
  pane, including the orchestrating agent. Workaround that worked: drive
  the session with the old build still in the store
  (`/nix/store/2qd228lh58rf32y16sr0d16hlcvcraiv-herdr-0.8.2/bin/herdr`;
  `ls -d /nix/store/*herdr-0.8*`). Restart herdr at a natural break.
- 2026-10-06 — **Draft manifests never go under `k8s/apps/`**: every
  directory there is an auto-synced ArgoCD Application (listed in
  `k8s/clusters/homelab/cluster.yaml`, selfHeal), so a committed draft
  deploys on push. Spike drafts park in
  `docs/spikes/<slug>/` and are promoted with `git mv` (see
  `docs/README.md`).
- 2026-10-06 — **A stray `~` file at the repo root** was an editor
  backup of `auth notes` (a `~`-suffix written as a bare name). Deleted
  with the original; if it reappears, it is the editor, not a tool.
- 2026-10-08 — **Draining nas1 and cp1 both stall on PDBs, by design.**
  nas1 holds the only replica of the media volumes (`movies`,
  `downloads`, `tv`), so Longhorn's `block-if-contains-last-replica`
  pins its instance-manager PDB at 0; cp1 is the only node KubeVirt's
  `virt-api`/`virt-controller`/`virt-exportproxy` may run on (required
  control-plane affinity, `bhui`), so their PDBs do the same. Either
  wait for Talos's 5-min `DrainTimeout` (it proceeds; media pods go
  down with the node, which single-replica means anyway) or, for
  nas1, flip `settings.longhorn.io/node-drain-policy` to `always-allow`
  for the drain and **put it back** — it is the chart default, not in
  git. After cp1 returns, expect ~1 min of 2-replica volumes
  `degraded` (rebuild) and cdi/csi-provisioner CrashLoopBackOff
  (leader election through the API blip) — both self-heal; evicted
  pods leave `Error` corpses to delete.
- 2026-10-08 — **Rebuilding the installer for an extension change
  needs no nodeagent binary**: `build.sh --installer-only` reuses
  `ghcr.io/marnyg/p0agent:<manifest version>`; bump `SUFFIX` in
  `installer.env` so the tag changes. imager runs under amd64
  emulation on the Mac in ~3 min.
- 2026-10-10 — **A kata pod's stdin is a pipe that never closes**
  (`/proc/1/fd/0 -> pipe:[…]`, no `stdin: true` needed to get it), so a
  program that reads stdin to EOF — `pi -p` prepends piped stdin to the
  prompt — hangs forever with no output. The agent entrypoint redirects
  `</dev/null`; anything else that runs non-interactively under kata
  must too. `docker exec` on the nixos box closes stdin, so the smoke
  there did not catch it.
- 2026-10-10 — **`kubectl apply` of a git-owned object is undone in
  seconds**: the root Application's selfHeal reverted a hand-applied
  CronJob image pin to the revision ArgoCD knew, and the next
  `agent-run.sh` ran the old image. Wait for the poll (~3 min; watch
  `kubectl -n argocd get application apps -o
  jsonpath='{.status.sync.revision}'`) instead of applying.
- 2026-10-10 — **A new GHCR package is private by default** (anonymous
  manifest GET → 401; nodes cannot pull). Flip it at
  github.com/users/marnyg/packages/container/<name>/settings before
  the first pod needs it — `sandbox-agent` needed this; `sap-actors`
  and `gateway` were done the same way.
- 2026-10-10 — **`scripts/agent-run.sh` is how an agent runs**; the
  suspended CronJob `agent` in `ai` is only the template. A Job that is
  still `Running` with nothing in its log for more than ~a minute is
  wedged (see the stdin note), not thinking; `kubectl -n ai exec job/<n>
  -- sh -c 'cat /proc/net/tcp; ls -la /proc/1/fd/0'` tells which.
- 2026-10-11 — **`bd` can stall past 20 s on a plain `note`** (seen
  once; the retry landed in <5 s). Wrap `bd` mutations in `timeout 25`
  and re-check with `bd show` before assuming the write happened.
- 2026-10-11 — **The default kube context on the Mac is `k3d-grove`, an
  unrelated local cluster** that also has a Helm Forgejo in ns
  `forgejo`. The homelab is `KUBECONFIG=$REPO_ROOT/kubeconfig` (context
  `admin@homelab`; gitignored). Export it before any `kubectl` — a
  swarm worker's dry-run hit the wrong cluster and read "selector
  immutable" as a manifest bug.
- 2026-10-11 — **`git diff` here runs `difft`** (`diff.external`), so
  any `grep`-counted check must use `git diff --no-ext-diff`.
- 2026-10-11 — **Image builds need the NixOS box**: `HUB_BUILDER=mar@nixos
  k8s/apps/sandbox/build.sh` (no x86_64-linux builder on the Mac;
  `ghcr-push.sh` builds in that store over ssh and pushes from there).
- 2026-10-11 — **ArgoCD's git poll can lag well past 3 min**; a
  `argocd.argoproj.io/refresh=normal` annotation on `application/apps`
  only re-reads git (not a hand-apply) and is the safe nudge.
- 2026-10-11 — **Anthropic monthly spend limit hit mid-swarm** (429 on
  the `a-agent-run` worker after its commits, before its report). Check
  the account before launching a wave; workers die silently otherwise.
- 2026-10-11 — Forgejo: `admin` is a reserved username; the local
  break-glass account is `sysadmin` (password in the owner's manager).
  The `agent` token has `write:repository,write:issue` only — `/api/v1/user`
  returns 403 with it (needs `read:user`); check against a repo instead.
- 2026-10-12 — **A stale Go `vendorHash` passes a plain `nix build
  .#X.goModules`** (fixed-output cache hit); only `--rebuild` — what
  `.githooks/pre-push` runs — reports `specified:/got:`. Both
  `config-server` and `actors` hashes moved this session when
  `protocol/facethttp` appeared. Check with `--rebuild` after any
  change under a vendored tree.
- 2026-10-12 — pi `--no-session` makes `get_state.sessionFile` empty;
  the child starts pi with `--session-dir <tmp>` so the JSONL exists to
  ship at settle (verified against pi 1.0.3 by `a-pirpc`).
- 2026-10-13 — **A swarm wave that edits `protocol/cert` strands three
  vendorHashes** (config-server-bin, iroh-transport, actors-bin): the
  pre-push hook `--rebuild`s each `goModules` (minutes each). Run the
  hook's loop in the background right after the merge, not inside
  `git push`; log at `/tmp/swarm/vendorhash.log` this session.
- 2026-10-19 — `curl https://<x>.gw.mesh.internal` from the Mac fails
  with exit 60: curl (nix/homebrew build) reads its own CA bundle, not
  the keychain the mesh root was added to. `-k` for acceptance checks;
  the browsers trust it.
- 2026-10-17 — `kubectl exec <pod> -- python3 - <<EOF` runs python on
  an EMPTY stdin without `-i`; it exits 0 and nothing happens. Cost
  one wrong conclusion ("HA overwrote my edit") this session.
- 2026-10-17 — HA 2026.x `http:` YAML is a one-shot migration into
  `/config/.storage/http` (pending trial, 5 min to promote from the UI
  or it is dropped for good and YAML ignored). Edit the store before HA
  starts (the init container does), never YAML.

- 2026-10-20 — **An *arr download-client POST validates the category
  against the running nzbget**, not the ConfigMap (`Category does not
  exist`): nzbget's conf is rendered at pod start, so a new category
  needs `kubectl rollout restart deploy/nzbget -n media` before the
  consuming configurator runs. Cost one chaptarr pod restart.
- 2026-10-20 — Prowlarr's application PUT fails while the target *arr
  is still booting (it validates live); the configurators retry POST
  but not PUT. A failed `Refreshing Prowlarr application` after a pod
  roll is usually that — rerun by deleting the pod.

- 2026-10-17 — **Two sessions shared this worktree**; a parallel
  session's commits (`6ac688a`, `20b49db`, homeassistant messages)
  swept up the forgejo-runner files staged here. Commit with explicit
  paths (`git commit -m … -- <paths>`), never `git add -A`, while
  another session may be committing.
- 2026-10-17 — **Reading Forgejo Actions logs**: the web log routes
  reject basic auth, and the mirror repo 404s to anonymous callers
  (the `mirrors` org is not public). Finished logs are at
  `/var/lib/gitea/actions_log/mirrors/talos-config/<nn>/<task>.log.zst`
  in the forgejo pod. The task id is not the run number:
  `ls -t …/*/*.log.zst | head -1`. While a run is live, read
  `/proc/*/cmdline` in the runner pod (no `ps` in the image).
- 2026-10-17 — **Runner lifecycle**: the nix store lives in the runner
  pod's overlay, so runs on the same pod are warm (about 1 min). A
  ConfigMap change needs `rollout restart deploy/forgejo-runner`, and
  that kills any run in flight: Forgejo leaves it "running" until the
  zombie reaper. Start a fresh one with
  `POST /repos/mirrors/talos-config/actions/workflows/nix-cache.yml/dispatches {"ref":"main"}`
  (sysadmin). The mirror's first import fires no push event; force a
  sync with `POST …/mirror-sync`. Attic's ConfigMap isn't hashed into
  its pod either: `rollout restart deploy/attic`.
- 2026-10-17 — **Docker on mar@nixos**: container DNS lookups take
  exactly 5 s (one of the parallel A/AAAA replies gets lost), which
  matches nix's old connect-timeout. Use `--dns-opt single-request`;
  kube-dns doesn't have the problem. Smoke tests there must use the
  pod's caps (`--cap-drop ALL --cap-add …`): the default-caps smoke
  test missed two runner bugs.
- 2026-10-17 — New ghcr packages default to **private**, and only the
  browser can change that (Danger Zone; you must type the package name
  to confirm). A node pull gets 401 until then.
- 2026-10-18 — **A new `k8s/apps/<name>` directory deploys nothing until
  it has a line in `k8s/clusters/homelab/cluster.yaml`** (ADR-0040).
  Conversely, deleting a line deletes the directory's resources (the
  ApplicationSet adds the resources finalizer). Per-child ops:
  `argocd app terminate-op platform-<name>`; the 2026-09-29 recipe
  for the root no longer applies to workloads. The six Helm charts are
  still their own Applications (`longhorn`, `loki`, …) nested in
  `platform-<dir>`.
- 2026-10-17 — **iroh-relay with `[access.http]` needs a CA bundle at
  startup** (5gz): it builds a reqwest client before serving and panics
  (exit 101, "No CA certificates were loaded from the system") when
  none is found — even though our hook URL is plain-HTTP loopback. The
  fly image ships `cacert` (`fly/image.nix`), so production is fine;
  the nix sandbox has none, which is why `TestRelayChild/hooked`
  points `SSL_CERT_FILE` at a throwaway self-signed PEM. Anywhere else
  the hub runs the relay with a hook (a dev box, a future Talos
  extension), the environment must carry CA roots or the child
  crash-loops and `/status` shows the relay DOWN.
- 2026-10-17 — **The relay gate is wired but observe-only** (same
  evening, `82d496c`): the first warm gate denied `sap-provisioner` at
  its reconnect — the sovereign-actor plane (provisioner, lighthouse,
  laptop parent, every child) holds protocol keys, not member certs,
  and there is no other relay (ADR-0022). `relayAdmitter.gate=false`
  admits everyone past the cold window; the `/status` relay line says
  `observing (N members; non-member admitted)` after such a connect and
  `gated (N members)` after a member's. The old failure mode (a member
  off the name map refused until it re-mints) is parked with the gate;
  spike `qx5c` decides how actors get admitted before it is enforced.
  A hub deploy re-seals it: sign at `/status` afterwards.
- 2026-10-17 — **nix2container `perms` regexes see the STORE path**
  (`/nix/store/…-sandbox-agent-scratch/tmp`), not the image path:
  `^/tmp$` matches nothing, silently. And a `perms` entry refuses a
  tree that shares a directory with another `copyToRoot` entry (`/etc`
  via `cacert`), so writable dirs get their own derivation
  (`k8s/apps/sandbox/image.nix` `scratch`). Verify on the box:
  `docker run --rm --entrypoint /bin/sh <image> -c 'ls -ld /tmp'`.
- 2026-10-17 — **Agent-child acceptance recipe** (e6pu, passed): build
  `nix build .#actors-bin -o /tmp/actors-bin` on the Mac, then
  `spawn -state ~/.sap-parent -lighthouse ed:5cad808a… -provisioner-id
  ed:9c3ae5ec… -image ghcr.io/marnyg/sandbox-agent@sha256:<digest>
  -window 4m -kit-ttl 6m -session-dir DIR -prompt "…"`; the digest
  must equal the provisioner's `-outfit` key or the child boots with
  no `OPENROUTER_API_KEY`. Birth takes ~110 s: the image pull (every
  new tag re-pulls ~450 MB on this cluster) — the default `-window 3m`
  is marginal on a cold node. Events print as `event <child> <json>`;
  the run ends at `child_settled`. The parent exits when the lease
  lapses (`-renewals`), a few minutes after the task. **With
  `-repo owner/name`** (e6pu.3) the outfit must also carry
  `forgejo-agent` or `#task` is refused ("no forge"); the pointer is
  the `child_settled` line; read the PR with the bot token
  (`kubectl -n sap get secret forgejo-agent …`) — the API 404s without
  auth on a private org even through the gateway.
- 2026-10-18 — **A new file is invisible to `nix build` until `git add`**
  (flake source = the git tree): `actors/child/workspace.go` built fine
  with `go build` and failed inside `nix build .#actors-bin` with
  "undefined: Forge" until staged. Stage before the tagged gate.
- 2026-10-18 — **A Gatus endpoint pages only if it lists
  `alerts: [{type: ntfy}]`** — the provider's `default-alert` fills in
  thresholds but never adds the alert itself, so a new row in
  `k8s/apps/gatus/config.yaml` without that line is green on the page
  and silent on the phone. ntfy (`k8s/apps/ntfy`) has no auth: any
  `admins` device may publish to `alerts`, any pod may subscribe. The
  Android app must be on "instant delivery" (no Firebase upstream);
  if it refuses the meshtls user CA, subscribe at `http://ntfy.gw…`.
- 2026-10-22 — **The parent's store migrates in place on first open of
  the task-layer build**: `sessions.task` (the child's run id) is
  renamed to `run` and a new `task` column added; `flows` seeds the
  default flow. One-way. Snapshot the `sap-parent` PVC before rolling
  the image while `bsj` is open. A fresh store gets both columns from
  the schema and skips the ALTER.
- 2026-10-22 — **A task's agent prompt is one `#task` prompt**: persona
  text + frame + exit contract, rendered by `Flow.RenderPrompt`. To
  read what a persona actually saw, open the session (`/s/<id>`) the
  task's history links — the prompt is stored verbatim.
- 2026-10-19 — **`talos/pins/` is read on every served git tip, not
  only at startup**: a commit that adds a rotation (or removes a file
  to re-pin) changes who may unseal within one git poll, and a
  rotation that un-admits the current speak-as issuer **re-seals the
  hub identity** — beats stop until an admin signs a fresh proposal at
  /status. Keep that in mind before committing a rotation: do it when
  you can unseal right after. The file name must be
  `<scheme>-<address>.json` (`eth-0x…`), lowercase, or the hub logs
  `git: pins: …` and keeps the previous pins.
- 2026-10-19 — **OpenRouter credits gate the whole task layer.** A
  balance below the child's `max_tokens` (128k) is a `402 billing_error`
  on the first call of every run; the parent now parks such a task at
  once as `not retried, model error: 402 …`. Check the balance before
  blaming a persona. The retry backoff is `-backoff` on `sap-parent`
  (1 min doubling); a retrying task shows `session: ""`, `retries > 0`
  and its error on the board until the beat respawns it.
- 2026-10-19 — **`git push` takes ~4–5 min after any `actors/**/*.go`
  change**: `.githooks/pre-push` lists it as a vendored input (it is
  not — nothing `replace`s `../actors`) and then realises all four
  `goModules` FODs twice, `--rebuild` re-fetching modules. Fix: drop
  `actors/**/*.go` from the trigger glob and check only the attrs an
  input feeds (`protocol/`, `iroh-transport/`, `iroh-go/iroh` →
  config-server-bin, iroh-transport, actors-bin; `<mod>/go.*` → that
  module's attr). Applied the same day — an actors-only push is now
  hook-silent; a `protocol/` push checks three attrs, not four.
- 2026-10-19 — **`actors/build.sh` can evaluate a stale rev.** Right
  after a commit, `nix eval` once returned the previous HEAD's
  `shortRev` and the script rebuilt and re-pushed the old tag (same
  digest, so harmless). Read the `building ghcr.io/…:<tag>` line
  against `git rev-parse --short HEAD` before pinning; re-run if they
  differ.
- 2026-10-19 — **Argo did not pick up the `sap-parent` pin within
  the poll**; `kubectl -n argocd annotate application
  platform-sap-parent argocd.argoproj.io/refresh=normal --overwrite`
  synced it in seconds.

- 2026-10-24 — **`tasks` crashloops on purpose if its log stops replaying.** The pod rebuilds all state from the event log in `tasks.db` at boot and `log.Fatalf`s on the first event a reducer rejects. That is a reducer bug (or a payload-shape change without an upcaster), never a reason to edit or truncate the log (`tasks/docs/desired-state/domain-model.md`, invariants 1 and 6).
