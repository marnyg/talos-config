# ADR-0046: Syncthing is a gateway facet; the cluster is the always-on peer of every device's sync set

- Status: Proposed 2026-11-07 (owner). Live when: the `syncthing`
  row compiles into an admin's bundle, this desktop's Syncthing
  connects to `syncthing.gw.mesh.internal:22000` and the first folder
  reaches `Up to Date` on both sides.
- Date: 2026-11-07
- Related: spike `talos-config-ch74` (share vs sync — this is the
  sync half; the SMB share `files/transfer` stays for VMs, `e6yj`),
  epic `talos-config-7lxt`; ADR-0017 (+ kind ≡ facet vocabulary),
  ADR-0038 (`ssh`: raw TCP + service login as the second gate), the
  `rdp` facet (same shape on the gateway), ADR-0031 (the bulk tier
  and what "disposable" meant), ADR-0044 (encrypted-at-rest +
  Tigris — this ADR opts a class *out* of the backup half), ADR-0011
  (durability belongs on state, not the library).

## Context and Problem Statement

The owner's files from four retired disks (`~/disks/{1TB-2024,
1TB-old,256GB,128GB}`, ~450 GB before dedup: an `archive`, a
Nextcloud export that largely duplicates it, school/work, a bachelor
thesis, an old `$HOME`) need a durable home, and the owner's devices
(this desktop, the Mac, the phone) need a way to keep a working set of
them in sync. Steam libraries and the old OS root are not preserved;
git repositories go to Forgejo (`7lxt.5`), not into a sync folder.

Spike `ch74` asked whether a network share or a sync flow is the
model for machine↔machine files. The share answered the VM case
(`\\samba\transfer`, where the consumer is a guest on the pod
network). For devices the share is wrong: they are offline, remote,
and on the phone a mounted share is not a thing. Syncthing is the
sync flow — but a Syncthing peer must be reachable by every device,
and the mesh has one way to reach a service on the cluster: a facet.

## Decision Drivers

- **Devices reach the cluster only through the mesh** (ADR-0025/0033:
  the device has a tun; the gateway is the member in front of the
  cluster). A LAN hostPort like samba's 445 would be a second door,
  and remote devices would still need the mesh. One path, LAN or not.
- Syncthing's wire is TLS on TCP 22000 with its own device-ID
  pairing: a `rdp`/`ssh`-shaped **two-gate** case — the mesh grant
  says who may knock, Syncthing's pairing says who may sync.
- The cluster copy is **one of N copies**, not the backup of record:
  the owner's redundancy is the peers (desktop on a repurposed 1 TB
  disk, the phone and the Mac for the small folders). So the volume
  wants encryption at rest (invariant: nothing plaintext leaves a
  node's disk unencrypted that is not re-downloadable) but **not** the
  nightly Tigris upload, which would carry ~300 GB for the sake of a
  copy that already exists three times.
- The only tier with room for ~300 GB × 2 is nas1's bulk tier
  (2 × 4 TB, ~3 TB free each); the NVMe tier would spend 600 GB of
  app-state space on cold files.

## Considered Options

- **A — Syncthing on a hostPort on nas1 (samba's pattern).** Works on
  the LAN; a remote device needs Syncthing's global relays or the
  mesh anyway. Rejected: a second door for the same client.
- **B — a `syncthing` gateway facet, natural port 22000, forwarded to
  the syncthing Service.** One recipe row `{facet: syncthing, group:
  admins}`, one `-syncthing=` flag on the gateway, devices dial
  `tcp://syncthing.gw.mesh.internal:22000` with discovery and relays
  off. Chosen.
- **C — Syncthing on every device peering device↔device only, no
  cluster peer.** No always-on member: two devices that are never on
  at the same time never converge. Rejected as the model; devices may
  still peer directly on the LAN via Syncthing's local discovery as
  an optimization.
- **Storage i — `longhorn-encrypted` and hand-remove the backup
  label.** Rejected: the class's `recurringJobSelector` is the whole
  exclusion mechanism (storageclass.yaml); a hand-unlabelled volume
  is hidden state.
- **Storage ii — a new class `longhorn-bulk-encrypted`: 2 replicas on
  nas1's bulk disks, LUKS under the cluster passphrase, no backup
  group.** Chosen. Known cost from ADR-0029's day of mirroring across
  the bays: both replicas on one node, so a nas1 reboot faults both
  and rebuilds — accepted for a cold volume whose other copies are
  elsewhere.

## Decision Outcome

**B + ii.** `syncthing` joins the gateway's vocabulary
(`config-server/policy`, `verification/nickel/mesh-policy-v3.ncl`,
the recipe's `gateway:` scope), natural port 22000. The gateway
forwards it to `syncthing.syncthing.svc.cluster.local:22000`
(`k8s/apps/gateway`). `k8s/apps/syncthing` runs one Syncthing on a
`longhorn-bulk-encrypted` claim; its GUI is an ordinary
`ingress-http` service at `syncthing.gw.mesh.internal` behind the
admins gate, with Syncthing's own GUI password as the second gate
(Vaultwarden posture). Global/local discovery, relays and NAT
traversal are off on the cluster peer: it is addressed statically and
never dials out.

**The sync set is split by temperature, not one folder**: big and
cold (`archive`, media) lives on the cluster and on the desktop's
repurposed 1 TB disk; small and hot (documents, notes) on every
device including the phone. Folder names and ids are set once the
dedup (`7lxt.1`) shows the real structure.

**Order of operations**: dedup locally with `fclones` while the disks
are still mounted here, then let Syncthing itself do the initial
load (LAN speed, and the index ends up consistent) — no one-off rsync
pod. Git repositories are pushed to Forgejo and excluded.

### Consequences

- Good: one door for every device, LAN or remote; adding a device is
  pairing a Syncthing ID, nothing on the cluster side changes.
- Good: the bulk tier gains a non-disposable tenant for the first
  time, with the reasoning on record: the copies, not the tier, carry
  the durability. ADR-0031's "disposable" reading of the tier is
  unchanged for the media volumes.
- Neutral: Syncthing traffic is double-encrypted (QUIC on the mesh,
  TLS inside). Remote devices sync through the fly relay at
  ~50–75 Mbps (ADR-0006) — fine for the hot folders, not for the
  first load of `archive`, which happens on the LAN.
- Bad / to watch: a facet connection has no `ConnMaxAge` (as `apid`,
  `ssh`); Syncthing keeps one long-lived connection per peer, so the
  authorization ran once at connect. Acceptable for the same reason
  it is for ssh.
- Bad / to watch: the volume is not in the nightly backup. Losing
  nas1's two bays and the desktop's disk at once loses `archive`;
  that is the stated trade. If the owner's devices stop holding the
  cold set, this ADR's premise is gone — revisit, do not silently
  relabel.
- Ops: hub redeploy + unseal compiles the row into every admin's next
  `#bundle`; the gateway image is rebuilt for the flag.
