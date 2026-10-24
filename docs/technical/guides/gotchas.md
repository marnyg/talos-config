# Gotchas

Traps that have each cost real time at least once. Landed knowledge —
these are properties of the tools, not current weather (that lives in
`day-to-day/notes.md`).

## Talos

- **`127.0.0.53` in a talosctl error is the *node's* resolver, not
  yours.** Talos host DNS listens on 127.0.0.53 — the same address as
  systemd-resolved's local stub — so
  `dns: A record lookup error … on 127.0.0.53:53: server misbehaving`
  reads like a broken laptop resolver when the lookup actually failed
  on the node. `-e <endpoint>` is resolved locally; **`-n <node>` is
  resolved by apid on the node**. Cost an entire wrong diagnosis
  ("mesh DNS doesn't work locally") that survived into the docs, while
  `dig`, `curl`, `kubectl` and even `-e cp1.mesh.internal` resolved the
  same name fine. Check `get resolvers` on the node before blaming the
  client.
- **A reused data disk must be wiped before a `UserVolumeConfig` will
  take it.** Talos never overwrites a foreign partition table: on w2
  (2026-10-19) both Corsair NVMes still carried their Windows GPT
  (`Microsoft reserved partition` + NTFS), so the volumes failed with
  `no disks matched for volume (1 matched selector): 1 have not enough
  space` — the selector was right, the disk was full. Fix is live, no
  reboot: `talosctl wipe disk <dev>...` (only for disks the install
  did not touch — the install disk is wiped by the install). Same
  applies to a disk that was previously a Longhorn volume on another
  node, or any SATA drive pulled from a NAS.
- **`talosctl get disks` prints the NVMe serial; Windows prints the
  EUI-64.** Reading a still-running Windows box, `Get-Disk`/
  `Get-PhysicalDisk` `SerialNumber` is the namespace EUI-64
  (`6479_A740_…`, `0000_…_0026_B778_…`), not the controller serial
  Talos matches `disk.serial` against. Pin by **model** when the only
  measurement is from Windows (w2's hardware layer does; the model
  string was identical on both sides), and record the Linux serial
  after first boot.

## Nix / Go

- **`vendorHash` and untracked files.** Flakes only see git-tracked
  files, so a new package directory that is not yet `git add`ed is
  invisible to `go mod vendor` — its imports get silently left out of
  the vendor dir, and the build fails with `import lookup disabled by
  -mod=vendor` for a module `go.mod` clearly requires. **`git add`
  before recomputing the hash.** (Bit twice: `filippo.io/age`, then
  `nebderive`/nebula.)
- **The vendor derivation is fixed-output.** Nix reuses *any* store path
  matching the hash, so a stale-but-matching vendor dir survives a
  `go mod tidy` and keeps being reused. Symptoms are a missing package
  or "inconsistent vendoring" — *not* a hash mismatch. Force a recompute
  by setting a bogus hash and reading nix's `got:` line.
- **`go vet` does not treat `log.Fatal` as terminating.** Code after it
  is unreachable but unflagged, so a misplaced brace can orphan a whole
  block into dead code that compiles and passes every unit test. Found
  this way: the `WG_MASTER_KEY` auto-unseal, orphaned by a mis-scoped
  `else if`. Run the binary, not only the tests.
- **Never trust a hand-rolled encoding without an oracle.** A bech32
  generator constant typed from memory had one bit wrong
  (`0x26508e6b` vs `…6d`); only a round-trip test against the real age
  parser caught it.

## Networking

- **Hub netstack MTU is 1280**, so every WG client must clamp to ≤1240
  or TLS blackholes mid-handshake while pings still pass. `wgquick`
  output emits `MTU = 1240` for this reason.
- **kube-proxy in nftables mode** binds NodePorts to the primary node IP
  only. Fixed with `proxy.extraArgs.nodeport-addresses` plus a live
  DaemonSet patch — Talos renders bootstrap manifests once, so config
  changes do not reconcile them on a live cluster.
- **Any overlay under the test poisons a path measurement.** A VPN,
  Tailscale exit node or second mesh that carries the route to the peer
  becomes the underlay and hairpins the "direct" path (bit twice in the
  nebula era: wg0 up, then Tailscale). Pre-flight before believing a
  direct/relay verdict — portable, because `ip link` silently no-ops on
  macOS where these tests actually run:

  ```bash
  netstat -rn | head -5              # default route on a physical NIC?
  route get <peer-wan-ip>            # macOS: "interface:" must not be utun/tun
  ip route get <peer-wan-ip>         # Linux equivalent
  ```

  NAT type is classified by STUN binding requests from one socket to
  several destination IPs: same external port ⇒ cone, differing ⇒
  symmetric (relay is then a property of the network, ADR-0006).

## Kubernetes / Talos

- **Talos host DNS does not serve `/etc/hosts` to pods.**
  `extraHostEntries` + `hostDNS.forwardKubeDNSToHost` looks like it
  should give every pod the host's static names — it deliberately does
  not (siderolabs/talos#9822, #13141): kube-dns forwarding bypasses the
  hosts file. Pods that must resolve a mesh name (OIDC issuer fetches)
  need per-pod `hostAliases`. Also: Talos re-applies its bootstrap
  manifests, so hand-edits to the CoreDNS ConfigMap revert on reboot.

- **Changing `cluster.controlPlane.endpoint` rotates the ServiceAccount
  issuer.** Talos derives `--service-account-issuer` and
  `--api-audiences` from the endpoint. Every pod whose kubelet fetched
  a projected token *before* the apiserver flipped holds a token for
  the old issuer, and the kubelet never refreshes a token that is
  still within its (1-year) lifetime — so each API-using pod gets
  `Unauthorized` until it is recreated. P2.5 (2026-09-20) did this
  with 14 control-loop pods bounced by hand. Runbook, right after the
  endpoint apply (`FLIP` = the apply time, RFC 3339):

  ```bash
  FLIP=2026-09-20T18:15:00Z
  kubectl get pods -A -o json | jq -r --arg t "$FLIP" '
    .items[] | select(.status.startTime < $t)
    | select([.spec.volumes[]? | select(.projected.sources[]?.serviceAccountToken)] | length > 0)
    | "\(.metadata.namespace) \(.metadata.name)"' \
  | xargs -L1 kubectl delete pod -n
  ```

  Only pods with a projected SA token are touched (those are the ones
  that can talk to the API); pods under `automountServiceAccountToken:
  false` and the VMs are skipped. Watch for `Unauthorized` in
  controller logs afterwards — anything still failing was created
  before the flip and missed by the filter. Not verified: whether
  Talos accepts a second `--service-account-issuer` via
  `apiServer.extraArgs` to carry both issuers through a transition
  (kube-apiserver supports the flag repeated; Talos' `extraArgs` is a
  map, so at most one value). The endpoint changes rarely enough that
  the recreate is the runbook.

- **KubeVirt PCI passthrough on Talos needs ≥ v1.9.1.** v1.9.0's device
  plugin saw `/sys/bus/pci/devices/<addr>/vfio-dev/` and handed the
  launcher a `/dev/vfio/devices/vfioN` cdev path that the Talos kernel
  never creates (`CONFIG_VFIO_DEVICE_CDEV` unset — the class dir exists,
  the `dev` attribute does not); containerd then fails the compute
  container with `lstat /dev/vfio/devices/vfio0: no such file`
  (kubevirt#18895, fixed #18905). Also: `HostDevices` is **still a
  feature gate** in 1.9 — without it the VM webhook rejects
  `devices.hostDevices` outright. And a launcher pod that was already
  allocated by the old plugin keeps its bad device spec: restart the
  VMI after the handler rolls. (w2 → win2k25, 2026-10-19.)
- **The qemu guest agent runs as SYSTEM; per-user Windows state needs a
  hop.** `guest-exec` cannot map a drive or store a credential for
  Administrator. What works (win2k25, 2026-10-19): `guest-file-write`
  a `.cmd`, `schtasks /create /sc once /ru Administrator /rp <pw>
  /tr C:\x.cmd` + `/run`, read its log, delete the task and files. Note
  a scheduled task is a *batch* logon: persistent drive letters are
  not restored there (only interactive logons do), so verify with the
  UNC path, not `dir T:\`.
- **GeForce drivers on Windows Server: skip the installer, keep the
  INF.** NVIDIA ships no Server package for GeForce and the Game Ready
  installer refuses Server outright, but the INF inside targets NT 10.0
  — `7zr x <pkg>.exe Display.Driver` + `pnputil /add-driver
  nv_dispi.inf /install` binds the card with no reboot (win2k25,
  617.42, 2026-10-19; `k8s/apps/vms/win2k25-provision.yaml`). pnputil
  sits silent for ~5 min staging 2.7GB into the DriverStore — watch
  `setupapi.dev.log`, don't kill it. And download with `curl.exe`:
  PowerShell 5.1's `Invoke-WebRequest` crawls at ~2MB/s behind its
  progress bar and buffers the whole body in memory.
- **A DataVolume that omits `volumeMode` takes the StorageProfile's
  first matching set — state it.** The longhorn StorageProfile override
  (2026-10-18) listed RWO/Block first on a wrong assumption; the next
  win2k25 reinstall got a Block PVC and CDI's importer crash-looped
  (`blockdev: cannot open /dev/cdi-block-volume: Permission denied`
  — it runs non-root). Fixed both ends 2026-10-19: Filesystem first
  in the profile, `volumeMode: Filesystem` explicit in the VM's
  dataVolumeTemplate. A reinstall is the only thing that exercises
  this; the running VM's PVC predates the profile.
- **A KubeVirt `configMap` volume is a CD with the keys as files**,
  Joliet names intact (`provision.ps1` stayed `provision.ps1`) — the
  way to ship a reviewable script to a Windows guest next to the
  sealed answer file, which should carry secrets only.
- **A VMI spec change is not live.** Editing `hostDevices` (or most of
  `domain`) in the VirtualMachine only takes effect on the next boot of
  the VMI; with `runStrategy: Always`, `kubectl delete vmi <name>` is
  the restart (graceful via the guest agent). ArgoCD syncs the VM
  object, never the running instance.
- **ApplicationSet children un-pause themselves.** Removing
  `spec.syncPolicy.automated` from a `platform-*` Application by hand
  held for seconds: the ApplicationSet re-applies its template on every
  reconcile, and selfHeal scaled a workload back up mid-migration
  (2026-10-20). `ignoreApplicationDifferences` on the ApplicationSet
  (`k8s/clusters/homelab/cluster.yaml`) now keeps a hand-paused child
  paused; `scripts/encrypt-pvc.sh finish` is the resume.
- **A resumed ArgoCD app syncs the revision it last fetched, not
  HEAD.** Push, then resume automation at once, and selfHeal runs the
  *previous* commit against the live state; against a recreated PVC
  that is an immutable-field patch that fails and retries for
  minutes, and a hard refresh does not cancel it. Wait for
  `.status.sync.revision == HEAD` before re-enabling automation; a
  stuck op is cleared with `patch app --type=json remove /operation`.
- **A PVC moved by `kubectl apply` with `volumeName` must have
  `volumeName` in git too.** ArgoCD's three-way diff reads the field
  from last-applied, sees it absent in the manifest, and tries to
  remove it (immutable → sync error). Pinning it is also what makes a
  recreated claim rebind to its data instead of provisioning empty.
- **A Completed pod still pins its PVC.** `pvc-protection` counted a
  finished Job pod (kept by `ttlSecondsAfterFinished`) and a
  CronJob's 9-hour-old Completed run as users; the claim sat
  `Terminating` until the pod was deleted. Delete the Job, not just
  wait for it.
- **`fly storage create` attaches to the org's only app.** Even with
  `-o personal` and no `-a`, flyctl picked the hub app, set five
  `AWS_*` fly secrets on it and rolled the machine — the hub came back
  sealed. Unset the secrets (another roll) and unseal. Create the
  bucket with a throwaway app, or expect it.
- **`longhorn-csi-plugin` logs the encryption passphrase.** The
  `NodePublishVolume is called with req … secrets:{key:"CRYPTO_KEY_VALUE"
  value:"…"}` line is verbatim (1.12.0). Alloy drops it before Loki;
  anyone reading the plugin's pod logs directly sees it.
- **Longhorn skips recurring backups of DETACHED volumes by default.**
  `allow-recurring-job-while-volume-detached: false` logs a warning and
  moves on; a volume only mounted by a CronJob (libation-db), or an app
  scaled to 0, never gets backed up. Set true (application.yaml);
  `LonghornBackupStale` is the net if a volume slips again.
- **Longhorn exports no backup timestamps.** `longhorn_backup_state`
  says Completed/Error, not when; freshness comes from kube-state-metrics'
  custom-resource metrics over BackupVolume `status.lastBackupAt`
  (RFC3339 strings parse to unix seconds in ksm v2.20).
- **Restores from Tigris are latency-bound.** At the default
  `restore-concurrent-limit: 2` a restore pulled ~6 MB/s; 8 helped
  ~1.5x. A full restore of 19 GB is tens of minutes, not seconds — plan
  disaster recovery around that.
- **kubectl rides the gateway.** The kubeconfig's server is
  `cp1.mesh.internal:6443`, which the in-cluster gateway fronts: scale
  the gateway to 0 (or lose it) and kubectl goes with it, TLS handshake
  timeouts. Fallback context `homelab-lan` (same CA and user, LAN IP,
  SNI kept so the cert still verifies):
  `kubectl --context homelab-lan …`. The kubeconfig is regenerated by
  `talosctl kubeconfig`, which drops it; re-add with
  `kubectl config set-cluster homelab-lan --server=https://10.0.0.68:6443 --tls-server-name=cp1.mesh.internal`,
  `kubectl config set clusters.homelab-lan.certificate-authority-data "$(kubectl config view --raw -o jsonpath='{.clusters[?(@.name=="homelab")].cluster.certificate-authority-data}')"`,
  `kubectl config set-context homelab-lan --cluster=homelab-lan --user=admin@homelab`.
- **A graceful node shutdown leaves `Failed` pods behind.** The kubelet
  terminates pods "in response to imminent node shutdown" and they stay
  `Error` until deleted (the GC threshold is 12,500). Harmless, just
  noise: `kubectl get pods -A --field-selector=status.phase=Failed`.
- **GNU `sed -i ''` is not BSD `sed -i ''`.** On this laptop `sed` is
  GNU (nix); `-i ''` takes the next argument as the file and silently
  edits nothing. A "nothing to commit" after an in-place edit is this.

## OAuth / OIDC

- **`golang.org/x/oauth2` sends `client_id` as HTTP Basic auth first**
  (empty password, RFC 6749 §2.3.1) and only retries with form params
  after a failure. A token endpoint that reads only the form burns a
  single-use code on the Basic attempt and the retry dies on the dead
  code — every exchange fails with `invalid_grant` while logs show two
  different errors. Cost the first real ArgoCD sign-in; fixed in
  `siweoidc` by accepting both styles (`75fb4e7`).

## Supply chain

- **Any helm-repo Application is a rebuild time bomb.** The Bitnami
  chart purge 404'd `bitnami-labs.github.io/sealed-secrets` and silently
  killed the controller. Vendor release manifests instead.

- **A brand-new GHCR package is private**, and the pull failure looks
  like a cluster problem: `failed to fetch anonymous token: 401` →
  `ImagePullBackOff`. Nothing is wrong with the pod — flip it at
  `github.com/users/<owner>/packages/container/package/<name>` →
  Package settings → Change visibility. The API route (`PATCH
  /user/packages/container/<name>` with `visibility=public`) answered
  **404** with both the gh OAuth token and the ghcr docker token, even
  though `GET` on the same URL returned 200; the change did apply, so
  retry-then-check-visibility rather than trusting the status code
  (2026-10-22, `marnyg/vmpower`). Every other image here is public, so
  this only ever bites the first push of a new one.

## Operational

- **`fly logs` replays old buffered history** — filter by timestamp or
  you will debug yesterday.

## Hardware (promoted from notes 2026-10-24)

- **nas1's two 2.5GbE ports: keep the cable in `:a8`.** The MACs are
  consecutive and Talos reports the *first* port's MAC as the identity
  the device flow selects `talos/machines/<mac>/` by, whichever port
  has the cable. Cable in `:a9` ⇒ config served under `:a8`, whose
  `deviceSelector` pins the address onto a link-less port: console says
  `CONNECTIVITY OK`, DNS times out, trustd is "no route to host", the
  install wedges at `Installing`. `arp -n <ip>` from the LAN shows which
  MAC actually answers.
- **nas1's SMBIOS is the OEM placeholder** (serial `Default string`,
  UUID `03000200-0400-0500-0006-000700080009`). Seal/unseal work, but
  the UUID is not per-unit — a second board from that OEM shares the
  KMS allowlist entry. Never read it as an identity (ADR-0004).
- **w1 has no wired PCI NIC; its LAN is a USB r8152 dongle.** The
  machine dir is named after the Dell dock's pass-through MAC
  (`98-e7-43-11-97-b8`), the dongle is `0c:37:96:5d:26:c4`. A selector
  on the wrong MAC matches nothing and Talos falls back to DHCP
  *silently* (`talosctl logs controller-runtime | grep 'no matching
  network device'`; check `get addressspecs`). Swapping the adapter
  also renames the interface, and **flannel keeps waiting for the old
  name** — cross-node pod traffic was dead for 4.5 h while host↔host
  worked (Longhorn attach failures, pods in ContainerCreating). After
  any w1 reboot/address change: delete its `kube-flannel-*` pod and
  check the node's `flannel.alpha.coreos.com/public-ip` annotation
  equals its INTERNAL-IP.
- **The dedicated fly IPv4 (`213.188.219.215`) is the KMS port's.**
  Releasing it breaks disk unlock on the next node boot (shared v4 =
  80/443 only). Bead `os8s` is the move off it.

## Talos (more)

- **An extension that mounts under `/var` needs `depends: [service:
  cri]`**, or `talosctl upgrade`/`reboot` hangs at `teardownLifecycle`
  ("luks2-EPHEMERAL … still in use"). `talosctl services` shows the
  offender still Running; `talosctl service ext-<x> stop` unblocks it.
- **Talos restarts an extension service when its
  `ExtensionServiceConfig` is *updated*, not when it is first
  *created* under a running service.** After an apply that adds the
  document, restart by hand; after one that changes it, don't.
- **Talos ignores an `ExtensionServiceConfig` whose service is not
  installed** — the document stays inert in the stored config and an
  `apply-config` carrying it is accepted without complaint. A rollback
  to an image that has the extension brings it back configured.
- **`talosctl -n <node>` is dialed by the endpoint, never
  short-circuited**: on a control plane apid holds a client cert, so
  every `-n X` is a TLS dial to `X:50000` with SNI `X` from cp1. `-n`
  must be a name cp1 resolves *and* has in its apid SANs — the node's
  hostname (`talos-wu6-eib` for cp1 until `t7b2`), never
  `cp1.mesh.internal` or `127.0.0.1`.
- **Re-serving a machine config without the mesh, one browser click**:
  `POST /device/code -d client_id=talos-pxe -d mac= -d uuid=` → open
  `/status?user_code=…`, approve (one wallet signature) → poll `POST
  /token` with the `device_code` → `GET /config?mac=…` with the bearer
  → `talosctl apply-config`. The boot token inside is good 1 h.

## Nix / Go (more)

- **Touching `protocol/*.go` stales every consumer's `vendorHash`**
  (`config-server`, `iroh-transport`, `actors`), and the fixed-output
  cache hides it: the build then fails with an `undefined: actor.X`
  that looks like a code bug. The pre-push hook and CI's `vendor-hash`
  job exist for this; recompute with a bogus hash, not `go.sum` — the
  vendor dir is per *package*, so importing a new package from an
  already-required module changes the hash while `go.sum` stays
  identical.
- **`iroh-go/` builds are slow cold** (iroh-ffi 7–10 min, iroh-relay
  8 min, uniffi-bindgen-go ~45 min on an M-series; warm
  `nix build .#iroh-go-smoke` ≈ 45 s) and carry two upstream patches
  to keep when bumping iroh: the `fetchCargoVendor` User-Agent override
  (crates.io 403s the generic UA of the 2026-01 nixpkgs) and the
  `postPatch` that removes upstream's `.cargo/config.toml` `-fuse-ld=
  lld` pin. Hand-run `go build`/`go test` in `iroh-go/` or
  `iroh-transport/` needs `CGO_LDFLAGS="-L$(nix build
  .#iroh-ffi-static --print-out-paths)/lib"` (+ `IROH_RELAY_BIN` for
  the relay test). `nix flake check --impure` is the canonical full
  check (`--impure` for devenv); the `static`/musl link runs only on
  Linux (the NixOS box or CI).
- **`git pull --rebase` destroys merge commits** — it flattened all six
  `merge swarm/*` commits of the M2 swarm (content intact, SHAs in
  bead notes gone). `git pull --rebase=merges`, or fetch + inspect.

## Networking (more)

- **ingress-nginx is a ClusterIP only** (`xnat`), dialed by the gateway.
  To poke nginx off-mesh: `kubectl -n ingress-nginx port-forward
  svc/ingress-nginx-controller 8080:80` and send a `Host:` header.
  `curl http://<node>/` answering anything means hostNetwork is back.
  To see the identity headers a backend gets, a throwaway
  `traefik/whoami` behind an Ingress echoes `X-Mesh-*`; the same
  request from the LAN with `Host:` must show none.
- **Outside the home LAN every iroh path is the fly relay** (QAD off ⇒
  no WAN punch): ~50–75 Mbps. Never read a throughput number taken
  from outside as a design result; the agent's journal prints
  `paths=[*relay:…]` vs `*ip:10.0.0.x`. **Any overlay carrying the
  route to the peer poisons a punch measurement** (Tailscale exit node,
  another VPN) — `route get <peer>` must show a physical NIC.
- **A machinery `client.New` with ONE endpoint runs gRPC's DNS resolver
  before any `WithContextDialer`**: a name only the identity plane
  knows fails as `name resolver error: produced zero addresses`.
  `facetResolver` in `config-server` `bootstrap.go` shadows the scheme;
  any new machinery client over a facet needs it
  (`bootstrap_client_test.go` is the pattern).

## Hub on fly

- **Deploys are `fly/deploy.sh`, never `fly deploy`**: the image is
  nix-built (cgo), `fly.toml` has no `[build]`. From a Mac,
  `HUB_BUILDER=mar@nixos fly/deploy.sh` builds in that box's store
  (`--store ssh-ng://… --eval-store auto`; `--builders` does NOT work,
  the darwin daemon runs as root without your ssh key). **`talos/` is
  baked at image build**: any `talos/clusters/**` or `talos/machines/**`
  change is a deploy (~3 min) + an unseal before `nix run .#apply`
  serves it — batch git changes, one unseal per session.
- **Every deploy re-seals the hub** (unsealed material is memory-only,
  invariant 8); so does sealing on demand (`fly machine restart …`).
  `/status` asks for **two signatures**: the master message and the
  per-process hubkey speak-as proposal (useless against any other
  process). `/sealed` is 503 while the identity plane is sealed or in
  the nag window. The process is a hub iff `IROH_RELAY_URL` is set
  (`fly/entrypoint.sh` folds `--auto-bootstrap`/`--kms-advertise` under
  it); a wrong wallet at unseal reads `decrypting secrets … (wrong
  wallet or message?)`.
- **Live acceptance of a redeploy**: `fly logs | grep auto-bootstrap`
  and `talosctl -n <node> logs ext-p0agent` — `lost; beating` → `beat
  ok` on the node within ~40 s of the kill, `node-unknown` →
  `etcd-running` on the hub within one 30 s poll of the unseal. After
  the unseal the **first** mesh flow to the hub takes ~15 s (dial
  timeout on the dead hubkey, then a rebeat; rebeats are rate-limited
  to one/min per daemon) and the **name map is empty until each member
  beats** (up to 6 h; members keep their last map). Force a beat:
  `talosctl service ext-p0agent restart` / restart the desktop daemon.
- **`/status` and `/sealed` round runway to the nearest day**; the nag
  window is judged on raw seconds, so "30 d left, NAG" at the boundary
  is not a bug. **A device may not enroll under a machine's name or
  `hub`** (409 at the challenge). **`hub.mesh.internal` is not in the
  name map** — the daemon answers it from `hub.json` after the first
  beat; a `media` member is admitted on hub-http and 403'd on
  `/config` by the per-route gate.
- **The `p0agent` extension chain**: static agent via
  `nix build --store ssh-ng://mar@nixos --eval-store auto
  .#packages.x86_64-linux.config-server-static` (`bin/nodeagent`),
  `talos/extensions/p0agent/build.sh <bin> <ver>` prints the
  `tag@digest` to pin in **both** `talos/hardware/*.yaml`; Talos
  version + official extension refs live in
  `talos/extensions/installer.env` only. A scratch rootfs has no CA
  bundle — Go's HTTPS client needs the `/etc/ssl/certs` bind.

## Mesh daemon / identity plane (desktop)

- **The Mac member is a launchd daemon** (`org.nixos.talos-mesh`,
  `irohup -tun`, user `_talosmesh`, state
  `/var/lib/talos-mesh/marius-mac.iroh`, log `/var/log/talos-mesh.log`;
  `talos-mesh-enroll -reenroll|-rekey`; restart `sudo launchctl
  kickstart -k system/org.nixos.talos-mesh`). Never also run a
  foreground `irohup` on the same state dir — two endpoints, one
  NodeId. **It is built from the nixos flake's `talos-config` input**:
  a commit reaches it only after `git push` **and** `nix flake update
  talos-config --refresh && darwin-rebuild switch` in `~/git/nixos`;
  verify by the store path in the LaunchDaemons plist.
- **Probing mesh names on macOS**: `dig cp1.mesh.internal` ignores
  `/etc/resolver` and SERVFAILs — use `dscacheutil -q host -a name …`
  (what CGO clients see) or `dig @198.18.0.2`. `route -n get -net
  198.18.0.0/15`, not a `.0` host, to check the route. **Fake IPs are
  per-daemon-process**: a restart re-mints from `198.18.1.1` in
  first-lookup order; the 60 s TTL bounds staleness — never hard-code
  one. Reading iroh paths: `P0_LOG=debug`, grep `path::selected`
  (`Ip(a->b)` direct, `Relay(url)` relayed; the hub is always relay by
  construction, ADR-0022).
- **`talosctl`/`kubectl` over the identity plane**: `irohup` bridges
  `cp1/apid` and `cp1/kube-api` to loopback; talosctl needs `-e
  talos-wu6-eib:50000 -n talos-wu6-eib` with that name in `/etc/hosts`
  (apid's SANs name the node, never `127.0.0.1`); kubectl can use
  `https://localhost:6443`. **Enrolling a headless member**: `irohup`
  serves its signing page on the box's loopback — read the URL from its
  log, `ssh -N -L PORT:127.0.0.1:PORT`, open locally; the NodeId never
  travels. **Gateway enrollment lives in the pod log** (empty PVC ⇒
  `kubectl logs -n gateway deploy/gateway` prints the `/status?
  user_code=` URL; sign as `gw`, group media, within 10 min; the PVC
  `gateway-state` *is* the membership).

## Android / TV

- **The APK is not built by CI** (the AAR links `libiroh_ffi.a`
  cross-built for `aarch64-linux-android`, impure + unfree NDK, ~1 h
  cold). Build on the NixOS box from **`~/p24`, which is an rsync'd
  copy, not a clone** — sync the files you changed first or you ship
  the previous source; `android/README.md` has the steps (~30 s warm).
  The `android-latest` release asset is only as fresh as the last
  manual `android/publish.sh`.
- **adb from the Mac**: `nix shell nixpkgs#android-tools`; the Sony
  only enumerates after USB debugging is on *and* a re-plug.
  `adb install -r` over the **same** debug keystore keeps the member
  (`files/member/`); a different keystore (`INSTALL_FAILED_UPDATE_
  INCOMPATIBLE`) means uninstall, which takes the membership with it.
  `adb install -r` kills the VpnService and it cannot be restarted over
  adb — open the app, tap Connect. The debug build's log is readable
  without root: `adb shell run-as dev.marnyg.mesh tail cache/mesh.log`.
  Starting the mesh app evicts Tailscale (one VpnService). Wi-Fi →
  cellular takes minutes to recover (the hub fetch reuses the dead h2
  conn, 30 s per attempt); a home-Wi-Fi-off test over USB adb is a
  valid cellular test when `dumpsys connectivity` shows one LTE netId
  holding.
- **The Shield is driven over network adb** (`adb connect
  10.0.0.2:5555` after Network debugging, accept once on screen);
  leanback ignores `input tap` — `input keyevent DPAD_*`, read focus
  from `uiautomator dump`, `KEYCODE_ENTER` not `DPAD_CENTER` in an
  EditText. Its toybox has no curl/wget/nslookup, only `nc`, which
  closes on stdin EOF: `{ printf '…'; sleep 6; } | nc <ip> <port>`.
- **Jellyfin's libraries are read-only mounts in the jellyfin pod**;
  writes go through the *arr pods. `POST /Library/Refresh` with an
  admin token picks a file up in ~20 s. The Jellyfin *app* is required
  for Direct Play (Firefox has no MKV demuxer).

## Verification / tooling

- **The Quint models are the sharper spec** for ADR-0015/0017: five
  doc sentences were refuted by them (decisions `h3c zqw dvf syw
  6o1`; FINDING blocks in `verification/quint/*.qnt`). When glossary
  and model disagree, the model's header says which ruling applied.
  **Laws over many nondet dimensions hold vacuously** (`authorize.qnt`
  reached Accept with p≈2⁻¹⁵ until a boundary-biased generator was
  added) — mutation-test a new model before trusting `[ok]`; never
  `powerset()` a 25-element set in a generator. **rapid at 100 checks
  is too shallow for pair-faults** — `TestFaultPairSweep` in
  `protocol/cert` enumerates the fault space exhaustively; add a case
  there when a new law needs a specific pair.
- **`bd` traps, all silent** (1.0.3): `bd ready --exclude-type` is a
  no-op (`| grep -v '\[epic\]'`); `--deps blocks:<id>` makes the *new*
  issue block `<id>` (use `bd dep add <new> --blocked-by <id>`);
  `--parent` inherits the parent's status, so a child of an
  `in_progress` epic is born invisible to `bd ready`. **Beads config
  lives outside git and can vanish** (`.beads/config.yaml`,
  `metadata.json`): without it `bd` silently uses an empty DB named
  `beads` — `bd dolt show` must say `Database: talos_config`;
  `git ls-remote origin refs/dolt/data` must move after `bd dolt push`.
- **`pi -p` hangs after its answer when stdin is open**: `timeout -k 10
  420 pi -p --no-session "…" </dev/null`.
- **The NixOS box (`mar@nixos`)**: login shell is **fish** — wrap
  anything scripted over ssh in `sh -c`/`bash -lc`; no passwordless
  sudo; `~/disks/1TB-old` is 100 % full — never copy onto it; UDP
  41641 is Tailscale's; LAN peers punch in through nixos-fw via
  conntrack with no inbound rule.
