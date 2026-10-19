# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review).

     Pruned 2026-09-03: wg0-era and nebula-phase-1/2 history removed
     (struck-through entries live in git history before this date).
     Pruned 2026-09-16: Phase-0-gate-era cautions, stock Mobile Nebula,
     zitadel-era kubeconfig reason, duplicated gotchas.
     Pruned 2026-09-30: nebula-era facts (mesh/, meshIP, v2 recipe/blocklist,
     leaf re-mint, spike relay app), superseded milestones (ADR-0001/0018
     "not built", v2 enrollment, poisoned seq, irohup stranding), entries
     contradicted by later ones (DHCP cp1, w1 back, CI-built APK), and
     duplicate vendorHash/tagged-test notes (AGENTS.md carries those).
     Reference beads issues by id (`talos-config-xxx`). -->

## Read first

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
- 2026-09-22 — **nas1's two 2.5GbE ports are a trap.** `:a8` and `:a9`
  are consecutive; Talos reports `${mac}` — the identity the device
  flow selects `talos/machines/<mac>/` by — from the **first** port,
  `:a8`, no matter which one has the cable. Provisioned with the cable
  in `:a9`: the hub served the config under `:a8`, whose
  `deviceSelector` then pinned `10.0.0.74` onto a port with no link.
  Symptom is not obvious — the console shows the address and
  `CONNECTIVITY OK`, while DNS times out, `trustd` on `10.0.0.68:50001`
  is "no route to host", and the install wedges at `Installing` with no
  apid ever appearing. **Keep the cable in `:a8`.** Diagnose from the
  LAN with `arp -n 10.0.0.74`: it shows the MAC that actually answers.
- 2026-09-22 — **nas1's SMBIOS is the OEM placeholder** (serial
  `Default string`, UUID `03000200-0400-0500-0006-000700080009`). It is
  declared in `meta.yaml` and seal/unseal work, but the UUID is not
  per-unit: a second board from the same OEM would share the KMS
  allowlist entry, so deleting it would revoke both. ADR-0004 already
  says UUID deletion is not access control; this makes that concrete.
  Do not read that UUID as an identity anywhere else.
- 2026-09-20 — **Changing `cluster.controlPlane.endpoint` rotates the
  service-account issuer.** Talos sets `--service-account-issuer` and
  `--api-audiences` to the endpoint URL. Every projected SA token
  minted before the apiserver restarts with the new flags carries the
  old `iss` and is refused (`Unauthorized`) — and kubelet's default
  token extension makes those tokens 1-year, so kubelet never
  refreshes them. P2.5 hit this on cp1: kubelet restarted at the apply
  and re-fetched *all* tokens 2 s before the apiserver flipped. Only
  pods that talk to the API notice (control loops: kube-proxy,
  flannel, longhorn-manager/csi, kubevirt, ingress-nginx); media/
  gateway/share-manager pods do not. Remedy: delete the affected pods
  (`kubectl get pods -A -o jsonpath` by `startTime` older than the
  apiserver's `startedAt`, then grep their logs for Unauthorized).
  Decode a pod's token to check: `exec … cat
  /var/run/secrets/kubernetes.io/serviceaccount/token | cut -d. -f2 |
  base64 -d`. Bead `etzl` for making the endpoint-change runbook do
  this. Nodes rebooted *after* the flip are fine (w1 was).
- 2026-09-20 — **w1's static address selects the dongle's MAC, not the
  directory's.** `talos/machines/98-e7-43-11-97-b8` is the laptop's
  Dell pass-through address; the box has no wired PCI NIC (Wi-Fi only,
  undriven), and the r8152 dongle it runs on is `0c:37:96:5d:26:c4`.
  A selector on the directory MAC matches nothing and Talos falls back
  to DHCP *silently* — check `talosctl -n <ip> logs controller-runtime
  | grep 'no matching network device'` and that
  `get addressspecs` lists the declared address, don't trust the lease
  happening to be right. Swap the dongle → update the selector.
  A reinstall needs the Dell dock (or the dir renamed): bead `c4vd`.
  **After any address change on a node, delete its flannel pod** (same
  stale `public-ip` failure as the rename note below) and expect
  `talosctl -n <hostname>` via cp1 to hang for a while (`hyjv`; use
  the LAN IP).
- 2026-09-20 — **Phone Wi-Fi→cellular takes minutes to recover, and
  the phone's cellular at home is unusable for testing.** The tunnel
  re-underlays at once (`advertising <cell ip>`), but the agent's
  hub fetch reuses the Wi-Fi-era h2 connection and times out 30 s per
  attempt until it gives up on it (`rnfk`); cellular→Wi-Fi is ~30 s
  because the old conn gets a RST. Separately the cellular bearer
  churned netIds every minute or two (`dumpsys connectivity | grep
  'Active default'`) with plain `curl` dead — do the remote-media soak
  event from a place with stable signal. adb notes: the mesh log is
  `run-as dev.marnyg.mesh tail cache/mesh.log` (debug build);
  `svc wifi disable|enable` flips Wi-Fi; the phone locks itself after
  a few minutes — drive the Jellyfin app while it's awake. _(Later:
  the bearer was stable for the whole 3/3 soak session, so the churn
  was that afternoon's, not the phone's; a home-Wi-Fi-off test over
  USB adb is a valid cellular test when `dumpsys connectivity` shows
  one LTE netId holding for minutes.)_ Reading `mesh.log` during
  playback: Jellyfin's HTTP pool closes idle conns in batches every
  ~3–5 min, so bursts of `stream done … 5m0s` are normal, not a
  reconnect — `cat /proc/net/dev | grep tun0` growing is the
  playback signal.
- 2026-09-20 — **w1's LAN NIC is a USB adapter and its name is not
  stable across boots.** It came back from the last reboot as
  `enp0s13f0u1` / `10.0.0.71` (was `enp0s13f0u1u4` / `10.0.0.67`, the
  address the TV's LAN-direct path was verified against). _(Later the
  same day: that was an adapter swap, not a rename — the `.67` MAC was
  the Dell dock's pass-through address; see the entry above.)_ flannel on
  w1 kept waiting for the old name (`external interface … not found,
  retrying`), the node annotation still said `.67`, and **cross-node
  pod traffic was silently dead for ~4.5 h** while host↔host worked:
  Longhorn volumes with a replica on the other node could not attach,
  pods sat in ContainerCreating, and longhorn-manager kept deleting
  them "so that Kubernetes will handle remounting". Fix: delete w1's
  `kube-flannel-*` pod; check `kubectl get node w1 -o
  jsonpath='{.metadata.annotations.flannel\.alpha\.coreos\.com/public-ip}'`
  matches the node's INTERNAL-IP after any w1 reboot.
- 2026-09-20 — **ingress-nginx is no longer on the LAN** (`xnat`): a
  ClusterIP Service only, dialed by the gateway. To poke nginx without
  going through the mesh, `kubectl -n ingress-nginx port-forward
  svc/ingress-nginx-controller 8080:80` and send a `Host:` header.
  `curl http://10.0.0.68/` answering anything means someone put
  hostNetwork back.
- 2026-09-20 — **The gateway's two Jellyfin paths are two origins.**
  `jellyfin.gw.mesh.internal` (:80, `ingress-http` facet, through
  nginx) and `jellyfin.gw.mesh.internal:8096` (the raw `jellyfin`
  facet, no nginx — P2.3's appliance path) are different origins to
  every browser and to jellyfin-plugin-sso, which derives its
  `redirect_uri` from the request Host. Both must be registered in
  siwe-oidc's `-client=jellyfin=…` list (they are, since `be0c1d7`);
  an unregistered one fails as a silent bounce in the app, with a 400
  only visible by curling `/authorize` directly.
- 2026-09-20 — **The Mac cannot test the raw `jellyfin` facet.**
  `mesh-policy-v3.yaml` grants `jellyfin` to `group: media` only, and
  the laptop is `admins`, so `curl jellyfin.gw:8096` from the Mac gets
  `irohtransport: refused: not authorized` — the gateway working, not
  a bug. Test that facet from a `media` device (the phone), or use
  `:80`.
- 2026-09-20 — **The APK is not built by CI any more** (P2.4): the AAR
  links `libiroh_ffi.a` cross-built for `aarch64-linux-android`
  (`iroh-go/nix/android.nix` — impure, unfree NDK, ~1 h cold, then
  cached), which a stock GitHub runner cannot produce. Build on the
  NixOS box: `cd android && NIXPKGS_ALLOW_UNFREE=1 nix-shell --impure
  shell.nix`, then `IROH_FFI_ANDROID_LIB=<…>/lib ./build-aar.sh &&
  gradle --no-daemon assembleDebug && ./publish.sh`. The workflow still
  exists but is dispatch-only and fails fast without the `.a`.
  `adb` on the Mac: `nix shell nixpkgs#android-tools`.
- 2026-09-20 — **`--auto-bootstrap` now needs `--iroh-relay`** (P2.2,
  `49a7bdb`): the hub dials the control plane's `apid` over the
  identity plane, so a hub without a wan endpoint reports
  `no-identity-plane` and `main` refuses the flag combination. fly's
  entrypoint sets both from `IROH_RELAY_URL` (fly.toml) — keep that
  env when touching the deploy. ~~Until both nodes run `p0agent` ≥
  0.1.3 (the `z2go` agent), a hub redeploy shows `node-unknown` on
  `/status` for up to the old agents' 6 h beat~~ — satisfied
  2026-09-20: both nodes run 0.1.4, so a redeploy is one `MinRebeat`
  from being noticed.

- 2026-09-05 — **The Quint models are the sharper spec for <!-- stale? -->
  ADR-0015/0017.** Five doc sentences were refuted and ruled the same
  day (decisions `h3c zqw dvf syw 6o1`; FINDING blocks in
  `verification/quint/{authorize,runway,approval}.qnt` record the
  trace). When the glossary and a model disagree, check the model's
  header first — it says which ruling applied. <!-- stale? -->
- 2026-09-16 — **ADR-0024 (hub actors cut by key) is Proposed and <!-- stale? -->
  partly built** (2026-09-17: Issuer listens in-process, Enroll →
  `#mint-device`, relay child, `/.well-known`; 2026-09-18: `#bundle`
  and the iroh endpoint `e8d`; 2026-09-19: the name map — only
  Provisioner-as-actor is not). Decision `itb` (hub
  HTTP over a stream facet) is revised by `mdv`: `/hosts` and `/policy`
  will not exist over the mesh — don't build them; the beat is
  `#renew` + `#bundle`. <!-- stale? -->
- 2026-09-18 — **Touching `protocol/*.go` stales TWO `vendorHash`es** <!-- stale? -->
  (`config-server/nix`, `iroh-transport/nix`) and a cached FOD hides
  it — `nix build` then fails with an "undefined: actor.X" that looks
  like a code bug (bitten again 2026-09-19). Recompute both:
  `nix build .#config-server-bin.goModules --rebuild` and
  `nix build .#iroh-transport.goModules` (plain — `--rebuild` errors
  when the old output was never built locally); CI job `vendor-hash`
  catches it on push.

## Mesh v3 spike infra (scratch)

- 2026-09-15 — **Any Talos extension that mounts under `/var` needs <!-- stale? -->
  `depends: - service: cri`**, or `talosctl upgrade`/`reboot` hangs at
  `teardownLifecycle` ("luks2-EPHEMERAL … still in use"). Symptom:
  `talosctl services` shows `ext-nebula`/`ext-iscsid` Finished and the
  offender still Running; `talosctl service ext-<x> stop` unblocks it. <!-- stale? -->

- 2026-09-16 — **P0.2 scratch on the NixOS box was torn down at the <!-- stale? -->
  gate** (same day): `p0agent-standin`, `~/p0-jf/`, the `/data/p0test`
  compose bind, the "P0 Test" library are gone; `jellyfin` was
  recreated from the restored compose. Still true about the box: the
  1TB disk `~/disks/1TB-old` is **100 % full** — never copy onto it;
  the login shell is **fish** (`ssh … bash -s <<EOF` for scripts); no
  passwordless sudo; UDP `41641` is Tailscale's; **LAN peers punch in
  through nixos-fw via conntrack with no inbound rule** (P0.2 step 6).
  `mar@nixos:~/p0` is kept as the x86_64 builder: a single-branch clone
  at `fa003f8` + scp'd files — not a checkout of the branch; sync by
  `scp`, rebuild with
  `NIXPKGS_ALLOW_UNFREE=1 nix-shell --impure iroh-go/android-p0/shell.nix
  --run 'IROH_FFI_ANDROID_LIB=/tmp/android-ffi-result/lib ./build-aar.sh
  && gradle --no-daemon assembleDebug'` (`/tmp/android-ffi-result` is a
  nix GC root only as long as nobody runs `nix-collect-garbage`). <!-- stale? -->
- 2026-09-16 — **Phone testing = `adb`, from the Mac**: `nix shell <!-- stale? -->
  nixpkgs#android-tools`; the Sony XQ-BQ52 only enumerates adb after
  USB debugging is on *and* the cable is re-plugged. The Files-app
  installer fails silently ("The app wasn't installed") on the debug
  APK after the Play Protect prompt; `adb install -r` works. Starting
  p0mesh **evicts Tailscale** on the phone (one VpnService). The
  Jellyfin *app* is required for Direct Play (Firefox has no MKV
  demuxer and buffers forever while pulling the raw stream). Server
  side: `curl /Sessions` with an API token shows `PlayMethod`. <!-- stale? -->
- 2026-09-16 — **Outside the home LAN every iroh path is the fly relay** <!-- stale? -->
  (QAD off ⇒ no WAN punch attempted): ~50–75 Mbps to a phone. Do not
  read a throughput number taken from outside as a design result;
  `p0agent serve`'s journal prints `paths=[*relay:…]` vs
  `*ip:10.0.0.x` every 5 s while bytes move. At home the same setup
  went `*ip` within 5 s and held 97 Mbps avg (2026-09-16).

## Hub / mesh (as running) <!-- stale? -->

- 2026-09-17 — **The production hub relays iroh** at <!-- stale? -->
  `https://marnyg-talos-config.fly.dev` (`/relay`, `/ping`,
  `/generate_204` proxied to an `iroh-relay` child; `/status` has an
  "iroh relay" row; child logs are prefixed `relay|`, level via
  `RELAY_LOG`). It is **open** (`access = everyone`) until `5gz`, and it
  runs while the hub is sealed. `RELAY_DISABLE=1` on the fly app turns
  it off. Probe: `p0relay listen/dial -relay https://marnyg-talos-config.fly.dev`
  (relay path only from the owner laptop — see the Cisco note).
  **Baseline noise** (2026-09-20, measured across a redeploy, unchanged
  by it): the child logs `relay-http-serve:conn{peer=127.0.0.1:…}
  … Connection did not reach established state within timeout` at
  ~1.7/min steady — loopback peers, i.e. the hub's own proxy/health
  probes, not members. Compare against that rate before chasing it.
- 2026-09-17 — **Deploy footguns found the hard way:** the Docker <!-- stale? -->
  context is the *working tree*, not git — a gitignored
  `talos/extensions/*/_out` (145 MB) shipped into the image and
  overflowed `/dev/shm` at entrypoint `cp` (crash loop, "No space left
  on device"). `.dockerignore` now excludes `**/_out`; keep build
  outputs out of `talos/`. Locally, `docker run` needs
  `--shm-size=256m` for the same `cp`. And the image must carry
  `protocol/` beside `config-server/` (go.mod `replace`).

- Every fly deploy **re-seals the hub**: derived roles (mesh CA, KMS,
  enrollment, DNS) are down until a wallet unseal at `/status`. The
  mesh HTTP listener exists only post-unseal. `/sealed` returns **503
  on mesh startup failure** but never blocks the unseal itself (KMS
  rides the WAN, invariant 4). (`talos-config-fbb`; gets heavier under
  Mesh v3 — relay identity derives from the master.)
- After a hub redeploy + unseal, `hub.mesh.internal` resets
  connections for a minute or so: the daemon beats against the new
  hub NodeId within ~1 min of the unseal (`beat ok` in
  `/var/log/talos-mesh.log`), but the tun kept dialing the *old*
  NodeId for ~15 s after that (in-flight dials). Self-heals; wait for
  `hub/hub-http: connected to <new id>` before `nix run .#apply`
  _(2026-09-20; nebula-era version of this note: cp1 unreachable
  ~45–60 s while the lighthouse re-registered)_.
- **Any overlay carrying the route to the hub/peer poisons a punch
  measurement** (Tailscale exit node, another VPN) — nebula hairpins
  through it. Pre-flight: `route get <peer-ip>` (macOS) / `ip route
  get` must show a physical NIC.
- Home network has **no native IPv6** — the blocker on ADR-0006's
  revisit trigger (`talos-config-41b`, deferred under v3).

## Cluster / Talos

- **LAN addresses are declared, not leased** _(2026-09-20, P2.5)_:
  cp1 `10.0.0.68`, w1 `10.0.0.71`, in `talos/machines/<mac>/patch.yaml`
  by MAC; the cluster endpoint is `https://10.0.0.68:6443`. A **blank**
  node (maintenance mode, before its config is applied) still gets a
  DHCP lease — find it with a port-50000 scan; the static address
  takes over with the first apply. Adding a node needs no router
  access (decision `ebis`); a DHCP-pool collision is accepted, and its
  symptom would be a duplicate-address fight on the LAN.
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
- Use `--kubeconfig ./kubeconfig` from the repo root; `~/.kube/config`
  is not maintained.

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
  `virtctl stop/start` is futile under selfHeal — VM state is a git
  edit of `runStrategy`.
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

## Tooling

- `nix build .#config-server-bin` runs the full go test suite in its
  checkPhase: any failing test on HEAD blocks devshell rebuilds and
  deploys. The devshell's quint is 0.30.0 (flake pin) vs 0.32.0 on
  current nixpkgs — models run on both; `verification/quint/
  _apalache-out/` is gitignored scratch.
- 2026-09-05 — **Quint laws over many nondet dimensions hold <!-- stale? -->
  vacuously.** `authorize.qnt` with ~40 independent `oneOf`s reached
  Accept with p≈2⁻¹⁵; all 7 seeded mutants survived until a
  boundary-biased generator (valid scenario + ≤2 injected faults)
  was added. Always mutation-test a new model before trusting `[ok]`.
  Also: never `powerset()` a 25-element set in a generator. <!-- stale? -->
- 2026-09-06 — **`iroh-go/` builds are slow cold and need the UA <!-- stale? -->
  backport.** crates.io returns 403 to the generic User-Agent the
  flake's 2026-01 nixpkgs `fetchCargoVendor` sends; `iroh-go/nix/
  default.nix` overrides two lines of the vendor script (fixed-output,
  hashes unaffected) — delete it when nixpkgs is bumped past the
  upstream fix. Cold on an M-series: iroh-ffi 7–10 min, iroh-relay
  8 min, uniffi-bindgen-go ~45 min; warm `nix build .#iroh-go-smoke`
  44 s. Hand-run `go build` in `iroh-go/` needs
  `CGO_LDFLAGS="-L$(nix build .#iroh-ffi-static --print-out-paths)/lib"`. <!-- stale? -->
- 2026-09-06 — `nix flake check --impure` is the canonical full check <!-- stale? -->
  and is green since `81u` (18 YAML files yamlfmt'd, 0 semantic
  diffs). `--impure` is required by devenv, see the `flake.nix` header.
  yamlfmt quirk: a flow mapping that is the last sequence item before a
  comment gets a trailing `,}` — valid YAML, all parsers agree, ugly. <!-- stale? -->
- 2026-09-06 — **rapid at the default 100 checks is too shallow for <!-- stale? -->
  pair-faults.** In `protocol/cert` the m14 mutant (group rule as set
  overlap) died in only ~6 % of runs at 100 checks; the killing pair is
  ~0.06 % of samples. `TestFaultPairSweep` enumerates the model's fault
  space exhaustively (2424 scenarios, 0.7 s) — add a case there when a
  new law needs a specific pair. `check.sh verify` is now ~5.4 min
  (`approval` 162 s, `clock` 110 s).
- **`pi -p` hangs after completing its answer** when stdin is left
  open. Wrap non-interactive uses: `timeout -k 10 420 pi -p
  --no-session "…" </dev/null`.
- **Beads config lives outside git and can vanish.** `.beads/
  config.yaml` + `metadata.json` are gitignored; if missing, `bd`
  silently falls back to an empty DB named `beads` and reports "no
  open issues". Check `bd dolt show` says `Database: talos_config`.
  `export.auto` is `false`. Session-close check: `git ls-remote origin
  refs/dolt/data` must move after `bd dolt push`. Q-threads that are
  `blocks`-chained need `--force` to close with a reason. <!-- stale? -->
- 2026-09-20 — **Three `bd` traps found while grooming, all silent.**
  (a) `bd ready --exclude-type` is a **no-op** in `1.0.3 (dev)` — it
  is accepted without error and filters nothing, for `epic` and `bug`
  alike, so any type-filtered ready query is wrong without saying so.
  Epics therefore rank inside `bd ready` (`0bc`, `359` are P1 and
  outrank real work); `bd ready -n 99 | grep -v '\[epic\]'` is the
  workaround. (b) `bd create --deps blocks:<id>` points the **opposite**
  way from what it reads like: it makes the new issue block `<id>`, not
  depend on it. Use `bd dep add <new> --blocked-by <id>`, then confirm
  the new issue is *absent* from `bd ready`. (c) `bd create --parent`
  **inherits the parent's status** — children created under an
  `in_progress` parent are born `in_progress`, and `bd ready` excludes
  those, so a fresh task can be invisible in the ready queue from
  birth. Check `--status` after any `--parent` create.

## CI / orchestration

- 2026-09-12 — **Upstream iroh's `.cargo/config.toml` pins <!-- stale? -->
  `-fuse-ld=lld` for x86_64-linux** — the `iroh-relay` derivation
  `rm`s it in `postPatch`; keep that when bumping iroh. <!-- stale? -->
- 2026-09-13 — **`git pull --rebase` destroys merge commits.** The <!-- stale? -->
  repo's default rebase flattened all six `merge swarm/*` commits of
  the M2 swarm (content intact, SHAs in bead notes gone). Either
  `git pull --rebase=merges`, or `git fetch` + inspect before merging. <!-- stale? -->
- 2026-09-13 — `nix flake check --impure` now includes <!-- stale? -->
  `.#iroh-transport` (runs the iroh handshake suite, ~25 s warm).
  Hand-run `go test` in `iroh-transport/` needs both
  `CGO_LDFLAGS="-L$(nix build .#iroh-ffi-static --print-out-paths)/lib"`
  and `IROH_RELAY_BIN=$(nix build .#iroh-relay --print-out-paths)/bin/iroh-relay`
  (relay test skips without the latter). `.#iroh-transport-static`
  exists only on Linux; no Linux builder is configured on the Mac —
  the CI `static` job is the only place the musl link runs. <!-- stale? -->
- 2026-09-18 — **The next hub deploy asks for two signatures at <!-- stale? -->
  `/status`** (decision `ce8`): the master message as before, plus
  the hubkey speak-as proposal. The proposal is per wallet and names
  the process's key — sign it only on that page (or `cast wallet sign`
  over the exact `<pre>` text); a copy is useless against any other
  process. Headless: `curl -d signature=… -d speakas_signature=…
  /unseal`; either alone works, the second must be the same wallet.
  Since 2026-09-19 (`tqr`) `/sealed` is **503 while the identity
  plane is sealed or in the nag window** on a hub run with
  `--iroh-relay` (members depend on the hubkey); a dev run without it
  only reports.
- 2026-09-18 — **Hub deploys are `fly/deploy.sh`, not `fly deploy`** <!-- stale? -->
  (`e8d`): the image is nix-built (cgo hub), `fly.toml` has no
  `[build]`, and a bare `fly deploy` fails on purpose. From the Mac:
  `HUB_BUILDER=mar@nixos fly/deploy.sh` — builds in that box's nix
  store (`--store ssh-ng://… --eval-store auto`; the darwin daemon runs
  as root and cannot use your ssh key, so `--builders` does NOT work)
  and pushes from there. The box's login shell is fish — anything you
  `ssh` over must be wrapped in `sh -c`. Musl Rust artifacts are warm
  there (`iroh-ffi-static`/`iroh-relay` for `x86_64-unknown-linux-musl`).
  Fallback without the box: `hub-image.yml` builds on every push;
  `workflow_dispatch` with `push=true` needs a `FLY_API_TOKEN` repo
  secret (not set yet).
- 2026-09-19 — **Talos restarts an extension service when its
  ExtensionServiceConfig document is *updated*, but not when it is
  first *created* under a running service.** Corrected on w1 (qb5q):
  `apply-config` at 19:33:38 restarted `ext-p0agent` on its own (the
  service log's `stopped` is exactly the apply's timestamp) — the
  manual `talosctl service ext-p0agent restart` afterwards was
  redundant. The original cp1 observation was the create case: the
  service was already running with no file, and registering v1 left
  the old mount namespace in place. So: after an apply that *adds*
  the doc, restart by hand; after one that *changes* it, don't.
- 2026-09-19 — **Re-serving a machine config without the mesh, one
  browser click**: `curl -X POST …/device/code -d client_id=talos-pxe
  -d mac=<mac> -d uuid=<uuid>` → open `/status?user_code=…`, approve
  (one wallet signature) → poll `POST /token` with the `device_code`
  → `GET /config?mac=…` with the bearer → `talosctl apply-config`.
  The boot token inside is good for **1 h** from serve; the agent
  enrolls within a second of seeing the file, so upgrade first, then
  serve. Fully headless would need two CLI wallet signatures (SIWE
  login + approval nonce).
- 2026-09-19 — **`p0agent` extension chain now**: static agent from
  `nix build --store ssh-ng://mar@nixos --eval-store auto
  .#packages.x86_64-linux.config-server-static` (`bin/nodeagent`,
  ~24 MB; `scp` it over — `file` is not on the box, fish shell), then
  `gh auth token | docker login ghcr.io -u marnyg --password-stdin`
  and `talos/extensions/p0agent/build.sh <bin> <ver>`; its last line
  prints the `tag@digest` to pin in **both** `talos/hardware/*.yaml`
  (2026-09-20: `docker buildx imagetools inspect` — no `crane` here).
  Talos version + official extension refs live in
  `talos/extensions/installer.env`, nowhere else.
- 2026-09-20 — **A machinery `client.New` with ONE endpoint runs
  gRPC's DNS resolver** (`dns:///<name>`) before any
  `WithContextDialer`; a name that only the identity plane knows
  fails as `name resolver error: produced zero addresses` — on the
  hub it read as auto-bootstrap `unreachable` while the node was
  admitting the stream. `facetResolver` in `bootstrap.go` shadows the
  scheme per client; any new machinery client over a facet needs the
  same option, and `bootstrap_client_test.go` is the pattern.
- 2026-09-20 — **Live acceptance of a hub redeploy** reads from
  `fly logs -a marnyg-talos-config | grep auto-bootstrap` (`/status`
  needs a wallet) and `talosctl -n <node> logs ext-p0agent`: expect
  `connection to hub … lost; beating` → `beat ok` on the node within
  ~40 s of the kill, and `node-unknown` → `etcd-running` on the hub
  within one 30 s poll of the unseal. 2026-09-20: 42 s. A scratch rootfs has
  no CA bundle: Go's HTTPS client needs the `/etc/ssl/certs` bind the
  0.1.1 spec adds (iroh's relay client carries webpki roots itself).

- 2026-09-19 — **`talosctl`/`kubectl` over the identity plane**: run
  `irohup` (bridges default to `cp1/apid=127.0.0.1:50000` and
  `cp1/kube-api=127.0.0.1:6443`), then
  `talosctl --talosconfig talos/talosconfig -e talos-wu6-eib:50000 -n
  talos-wu6-eib …` with `127.0.0.1 talos-wu6-eib` in `/etc/hosts` —
  apid's SANs name the node, never `127.0.0.1`. For kubectl:
  `kubectl --kubeconfig <kc> --server https://localhost:6443` (the
  API server cert does carry `localhost`). Enrollment state lives in
  `~/.config/talos-mesh/<name>.iroh/` beside nebup's two files; one
  wallet signature enrolls both planes.
- 2026-09-19 — **After a hub deploy the name map is empty until each
  member beats** (up to 6 h). Members merge their own last map now, so
  bridges keep working; to reconverge *now*, force a beat on both
  sides: `talosctl service ext-p0agent restart` on the node, restart
  `irohup` on the desktop.
- 2026-09-19 — **Enrolling a headless member**: `irohup` serves its
  signing page on loopback of the machine being enrolled, so from the
  Mac run it over ssh, read the `http://127.0.0.1:PORT/TOKEN` line out
  of its log, then `ssh -N -L PORT:127.0.0.1:PORT mar@nixos` and open
  that URL locally. The NodeId is minted on the box and never travels.
  `mar@nixos` is a member now (`nixos`, `ed:c02908be…`).
- 2026-09-19 — **Reading iroh paths**: `P0_LOG=debug` and grep
  `path::selected` — `network_path=Ip(a->b)` is direct,
  `network_path=Relay(url)` is relayed. The hub is always `Relay`
  (relay-only by construction, ADR-0022). `bash -lc` is required for
  anything scripted over ssh to that box (login shell is fish).
- 2026-09-19 — **`/status` and `/sealed` round runway to the nearest
  day** (`issuer.RunwayDays`). Before this, a speak-as signed one second
  ago read "119 d left" for a 120 d cert — the nominal value was
  unreachable, and an exact-equality test flaked on it in the nix
  sandbox. The nag window is still judged on raw seconds, so
  "30 d left, NAG" is possible at the boundary and is not a bug.
- 2026-09-19 — **The Mac's mesh member is a launchd daemon now**
  (`org.nixos.talos-mesh`, `irohup -tun`, user `_talosmesh`, state
  `/var/lib/talos-mesh/marius-mac.iroh` — unreadable as the login
  user by design). Log: `/var/log/talos-mesh.log`. Re-enroll/rekey:
  `talos-mesh-enroll -reenroll|-rekey` (browser wallet; `-paste` for
  headless). Restart: `sudo launchctl kickstart -k
  system/org.nixos.talos-mesh`. It only runs while `kit.json` exists.
  Don't also run a foreground `irohup` with the same state dir — two
  endpoints on one NodeId.
- 2026-09-19 — **Probing mesh names on macOS**: `dig cp1.mesh.internal`
  does NOT go through `/etc/resolver` (dig reads `resolv.conf`) and
  will SERVFAIL; use `dscacheutil -q host -a name cp1.mesh.internal`
  (what CGO clients like talosctl/kubectl see) or `dig @198.18.0.2`
  for the resolver itself. `route -n get 198.18.1.0` misreports `.0`
  hosts as the default route even while the /15 forwards; ask
  `route -n get -net 198.18.0.0/15` (that is what `RouteIntact` does).
- 2026-09-19 — **`talosctl -n <node>` is dialed BY THE ENDPOINT, never
  short-circuited**: on a control plane apid holds a client cert, so
  `director.IsLocalTarget` is never consulted and every `-n X` becomes
  a TLS dial to `X:50000` with SNI `X` from cp1. So `-n` must be a
  name cp1 resolves *and* has in its apid SANs: its hostname (own
  `/etc/hosts`), or any member hostname now that
  `hostDNS.resolveMemberNames` is on. `-n cp1.mesh.internal` will
  never work (cp1 has no resolver for the zone); `-n cp1` will not
  until `t7b2` pins the hostname — today it is **`talos-wu6-eib`**
  (`meta.yaml hostname:` carries it for `apply`).
- 2026-09-19 — **Admin paths are nebula-free on the Mac.** talosconfig
  `endpoints: [cp1.mesh.internal]`, `nix run .#kubeconfig`, `nix run
  .#apply` (hub at `http://hub.mesh.internal` over the hub-http facet,
  nodes via `-e cp1.mesh.internal -n <hostname>`). The overlay
  `GET /config` is **gone** from the hub (decision `d3z3`); if you
  are on nebula only (`mar@nixos`, no tun yet), you cannot apply. Test
  the facet with `curl http://hub.mesh.internal/config?mac=<mac>`;
  admitted/refused lines are in the hub's fly log as `hub-http:`.
- 2026-09-19 — **`hub.mesh.internal` is not in the name map** — the
  daemon answers it from `hub.json` (the beat's hub record), so it
  resolves only after the first beat and goes with the hub record's
  expiry. A media member is *admitted* on hub-http (recipe row) and
  gets 403 on `/config`; that is the per-route gate, not a bug.
- 2026-09-20 — **After a hub redeploy + unseal, the first mesh flow to
  the hub takes ~15 s** (`DialTimeout` on the dead hubkey, then a
  rebeat: `renewed … at ed:<new>` + `beat ok` in
  `/var/log/talos-mesh.log`), every later flow is instant. If the first
  flow rides a *pooled* connection to the dead hub it can wait for
  QUIC's idle timeout instead; the second flow recovers. Rebeats are
  rate-limited to one per minute per daemon — hammering a down hub
  will not beat faster. A `dig @198.18.0.2 <unknown>.mesh.internal`
  also kicks a beat (same limit): expect `beat ok` lines in the daemon
  log after typos.
- 2026-09-20 — **The 09-19 "network dropped during the redeploy" was
  the home router's DNS**, not the daemon: `sudo log show` for
  21:36–21:43 shows mDNSResponder's queries to `10.0.0.1` via `en7`
  unanswered 21:40:02–21:40:58 (ICMP/route/resolver config untouched),
  recovering a minute before the daemon restart. A deploy watched with
  `ping 1.1.1.1` + `dig @10.0.0.1` + `dig @198.18.0.2` on 09-20 showed
  no loss at all. When "everything is down" during a deploy, check WAN
  DNS separately from mesh names before blaming the tun.
- 2026-09-20 — **The Mac daemon is built from the nixos flake's
  `talos-config` input** (`github:marnyg/talos-config`, pinned in
  `~/git/nixos/flake.lock`): a talos-config commit reaches the daemon
  only after `git push` **and** `nix flake update talos-config
  --refresh && darwin-rebuild switch` there. Verify with the store
  path in `/Library/LaunchDaemons/org.nixos.talos-mesh.plist` — an
  unchanged path after a rebuild means the input did not move.
- 2026-09-19 — **Fake IPs are per-daemon-process, not stable.** The
  table "only grows" within one run, but a restart re-mints from
  `198.18.1.1` in first-lookup order — w1 was `.1.1` before this
  session's restart and the hub took `.1.1` after. The 60 s answer TTL
  bounds the staleness, so never hard-code a fake IP or cache one
  across a daemon restart.
- 2026-09-19 — **`config-server-bin` vendorHash changes whenever
  `fakeip` (or anything) imports a new package from an already-required
  module** — the vendor dir is per-package. Recompute with a bogus
  hash; `go.sum` staying identical is not evidence it's unchanged.
- 2026-09-20 — **The Mac has no nebula plane**: `irohup -tun` runs
  with `-dns-upstream` empty, no nebula process, no `10.42.` address.
  Anything still named `*.cp1.mesh.internal` (only `jellyfin.cp1`, for
  the TV) is unreachable from the Mac — by design, not a fault.
- 2026-09-20 — **GHCR creates new packages private.** The first push
  of `ghcr.io/marnyg/gateway` left the pod in `ErrImagePull` until the
  package was made public in the browser (no API for visibility).
  Same for any new `ghcr.io/marnyg/<name>`.
- 2026-09-20 — **Gateway enrollment lives in the pod log**: with an
  empty PVC, `kubectl logs -n gateway deploy/gateway` prints the
  `/status?user_code=…` URL; sign as `gw`, group media, within 10 min
  (an expired flow restarts itself with a fresh code). The PVC
  `gateway-state` is the membership — deleting it costs one wallet act.
- 2026-09-20 — **Verifying the identity headers**: a throwaway
  `traefik/whoami` pod + Service + Ingress `whoami.gw.mesh.internal`
  echoes request headers; `curl http://whoami.gw.mesh.internal/` from
  the Mac shows `X-Mesh-*`, `curl -H Host:… http://<node>/` from the
  LAN must show none. The `media` namespace's PSS warns but admits it.
- 2026-09-20 — **The TV is driven over network adb, not a remote**:
  `adb connect 10.0.0.2:5555` after Settings → Device Preferences →
  Developer options → Network debugging (the first connect needs the
  on-screen "Allow debugging" accepted once). The Shield's leanback UI
  ignores `input tap` — navigate with `input keyevent DPAD_*` and read
  the focused node from `uiautomator dump`. `DPAD_CENTER` inside an
  EditText types `a` via the leanback IME; use `KEYCODE_ENTER`.
- 2026-09-20 — **The Shield's toybox has no curl/wget/nslookup**, only
  `nc`, and that `nc` closes the socket on stdin EOF — a piped HTTP
  request reads as `0B in` (looks like the server hung up). Hold stdin:
  `{ printf '...'; sleep 6; } | nc <ip> <port>`.
- 2026-09-20 — **The mesh app is debuggable, so its log is readable
  without root**: `adb shell run-as dev.marnyg.mesh tail -20
  cache/mesh.log`; membership lives in `files/member/`. Reading the
  log this way does not disturb playback, unlike opening the in-app
  Debug screen.
- 2026-09-20 — **Jellyfin Quick Connect can be authorized from the
  cluster** instead of a browser: auth as `admin` at
  `/Users/AuthenticateByName` inside the pod, then
  `POST /QuickConnect/Authorize?code=<6 digits>&userId=<id>`. Signs
  the device in as *that* account. _Superseded 2026-10-06 (ADR-0034):
  the qc sidecar does exactly this call for `media` devices, as their
  own user, never admin._
- 2026-10-06 — **A `ReverseProxy` hook that reads a body must own
  `Accept-Encoding`**: forward the client's `gzip` and the upstream
  compresses; `ModifyResponse` then sees bytes (`resp.Uncompressed`
  false). Delete the header on that request and Go's transport
  negotiates gzip itself and decodes transparently. Found by the TV
  (OkHttp sends gzip; `nc` and curl do not, so the laptop test and
  the unit tests passed).
- 2026-09-20 — **`android-latest` can lag the real APK.** The AAR
  build left CI at P2.4, so the release asset is only as fresh as the
  last manual `android/publish.sh`; check the asset size (v1 ≈ 94 MB,
  v3 ≈ 18 MB) before telling anyone to sideload it.
- 2026-09-20 — **Reinstalling the app is destructive and one-way**:
  the CI-signed v1 and the NixOS-built v3 have different debug
  keystores, so `pm install -r` fails `INSTALL_FAILED_UPDATE_INCOMPATIBLE`
  and the uninstall takes the device's nebula config with it.
- 2026-09-20 — **The NixOS box builds the APK from `~/p24`, which is
  not a git clone** — an rsync'd copy of the tree (`~/p0` is the old
  P0.2 clone, on a detached HEAD). Sync the files you changed
  (`rsync -a config-server/meshtun/ mar@nixos:~/p24/config-server/meshtun/`)
  before building, or you will ship the previous source. Warm, the
  rebuild is minutes, not the hour the cold cross build costs:
  `libiroh_ffi.a` for `aarch64-linux-android` and the gradle caches
  are already in the store.
- 2026-09-20 — **`adb install -r` over the *same* debug keystore keeps
  the member**: reinstalling the NixOS-built APK on top of itself left
  `files/member/` intact (name, key, kit) — only the tunnel had to be
  reconnected. The destructive case is v1→v3 (different keystores).
- 2026-09-20 — **Jellyfin's libraries are backed by read-only mounts
  in the jellyfin pod**; writes go through the *arr pods
  (`kubectl exec -n media deploy/radarr -c radarr -- …` sees
  `/movies` read-write). `POST /Library/Refresh` with an admin token
  picks the file up in ~20 s.
- 2026-09-20 — **The Mac daemon lags a talos-config commit by a
  `darwin-rebuild switch`**: bumping `~/git/nixos/flake.lock` is not
  enough, and nothing warns you — compare the store path in
  `/Library/LaunchDaemons/org.nixos.talos-mesh.plist` against a symbol
  you expect (`grep Counters $(nix-store -q --deriver …)`-ish) or just
  the plist's mtime.
- 2026-09-20 — **`jellyfin.gw.mesh.internal:8096` is unreachable from
  the Mac** (`marius-mac`, a node, not in `media`) while `hub` and the
  `:80` ingress both answer; the phone and TV reach `:8096`. Unproven
  guess: facet authorization is group-scoped. Use the `:80` ingress
  from the desktop.
- 2026-09-20 — **Sealing the hub on demand is a restart** (no
  endpoint; unsealed material is memory-only, invariant 8):
  `fly machine restart 7817426a194968 -a marnyg-talos-config`, then
  sign both proposals at `/status`. Measured cycle: nodes log `lost;
  beating` ~30 s after the seal (h2 ping interval), `beat ok` ≤40 s
  after the unseal, hub `etcd-running` within the next 30 s poll.
- 2026-09-20 — **Phone dev loop from the Mac**: `adb` is not on PATH —
  `nix shell nixpkgs#android-tools -c adb …`. The APK build on
  `mar@nixos` (`android/README.md` steps 1–2) is ~30 s with the store
  warm. `adb install -r` kills the VpnService and it **cannot be
  restarted over adb** (`am start-foreground-service` → "Requires
  permission not exported"); open the app and tap Connect. Go log:
  `adb shell run-as dev.marnyg.mesh tail cache/mesh.log`.
- 2026-09-20 — **P4.1 upgrade timings and one Talos fact**: `talosctl
  upgrade --wait` w1 ~3.5 min, cp1 ~7 min (drain; both nodes up this
  time). After an upgrade the node's stored config still carried the
  `nebula` ExtensionServiceConfig for an extension that no longer
  exists, and the later `apply-config` with the same document was
  accepted without complaint — **Talos ignores an
  ExtensionServiceConfig whose service is not installed**; the
  document is inert until P4.2 stops the hub emitting it. Rollback,
  if ever needed, is `talosctl upgrade --image <0.1.4 pin>` (git
  history has it) — nebula would come back configured, since the
  stored config never lost the document.
- 2026-09-20 — **The hub bakes `talos/` at image build** (`fly/image.nix`):
  any `talos/clusters/**` or `talos/machines/**` change needs
  `HUB_BUILDER=mar@nixos fly/deploy.sh` (~3 min) + an unseal before
  `nix run .#apply` serves it. Plan one unseal per session: batch the
  git changes, deploy once.
- 2026-09-21 — **A device may not enroll under a machine's name or
  `hub`**: the hub answers 409 at `/mesh/enroll/challenge` and refuses
  the mint if the approver types one on the card. The name map is
  witnessed, so nothing else would have stopped a second `cp1`.
- 2026-09-21 — **Hub deploy recipe unchanged, one flag fewer**: the
  process is a hub iff `IROH_RELAY_URL` is set (`fly/entrypoint.sh`
  folds `--auto-bootstrap` and `--kms-advertise` under it). A wrong
  wallet at unseal now fails with `decrypting secrets … (wrong wallet
  or message?)` instead of a CA-pin mismatch — same meaning.
- 2026-09-21 — **Enrollment message is v3** (`name, group, node,
  nonce`). A client built before `600d2d4` signs v2 and gets 403 at
  mint; the phone/TV APK, gateway image and Mac daemon are on that
  list until rebuilt (bd task). Existing kits renew regardless.
- 2026-09-21 — **The dedicated fly IPv4 is the KMS port's, not
  nebula's**: `fly ips release 213.188.219.215` would break disk
  unlock on the next node boot (shared v4 = 80/443 only). `os8s`.
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
