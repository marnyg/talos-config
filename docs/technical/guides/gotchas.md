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
