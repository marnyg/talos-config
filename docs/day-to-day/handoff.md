# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-31 (fourth session) — **the cluster stopped being single-node**
(`03a712a`, `b31933b`, `cf4aaca`). w1, an Alienware x15 R1, is a Ready
worker with an encrypted disk and a mesh identity.

- First non-controlplane machine, which is most of the work: the cluster
  layer had to split (`worker-cluster.yaml` + `worker-secrets.yaml`,
  every issuing key stripped) because Talos rejects a worker holding
  etcd config or CA keys. `hardware/alienware-x15.yaml`, `meta.yaml`
  with the uuid recorded *before* first boot so the KMS allowlist was
  durable from the start — `kms: unsealed disk key` on the first reboot
  confirmed it.
- The machine was un-inspectable before install (no apid while waiting
  on approval), so it installed via `diskSelector: size >= 100GB`,
  chosen to fail safe. Now pinned to `/dev/nvme0n1` — and the
  precaution mattered: `sda` is the 2.1GB USB boot stick, which the
  role template's `install.disk` default would have overwritten.
- Two fixes provoked by the change: `encrypt-secrets` matched secret
  files by exact name (a new one would have stayed plaintext), and
  `nix run .#apply` would have aborted on w1's empty `ip:` before ever
  reaching cp1.
- **Invariant 4 gained a scope note** (decision 46): a worker's kubelet
  reaches the API server over the mesh — kube-apiserver has no LAN SAN
  — so cluster membership now has a hard lighthouse dependency. Owner
  accepted; provisioning and admin recovery still may not depend on the
  overlay.
- **Storage direction changed**: ADR-0011 (Proposed) adopts Longhorn and
  supersedes ADR-0008. More nodes land within days, and the media
  library's contents are declared disposable, so the migration has no
  data-movement step and cp1 needs neither reinstall nor repartition.

## Loose threads

- **w1's repartition is committed but not applied.** `patch.yaml` (200GiB
  EPHEMERAL cap + 700GiB `longhorn` volume + `hostname: w1`) needs
  `fly deploy` → unseal → **bare** `talosctl reset` → approve. The hub
  was deployed and left **sealed** waiting on that unseal. This is the
  one legitimate use of the reset `guides/reinstall.md` warns against.
- **ADR-0011 is Proposed, not Accepted**, and it leaves one thing
  unresolved on purpose: invariant 2's carve-out names the `u-media`
  volume as the single deliberate instance of state outside git, and
  Longhorn's control-plane state is not recomputable from git either.
  That wording needs a decision before Longhorn lands.
- `guides/reinstall.md` is largely obsolete under ADR-0011 — its central
  warning protects a volume that will hold nothing unique.
- Media hostPath PVs still have no `nodeAffinity` (bug `0b374653`).
  Harmless only while the library is empty; the `nodeAffinity` stopgap
  was deliberately **withdrawn** rather than committed, since Longhorn
  deletes the whole mechanism.

## Suggested next steps

- Finish w1: unseal, wipe, approve, then verify the 200/700 split and
  that the node returns as `w1` rather than a new generated name.
- Decide ADR-0011 (Accept/revise) and the invariant-2 wording with it.
- When the new nodes arrive: build the schematic with nebula +
  iscsi-tools + util-linux-tools (task `ca77f427`) and onboard them on
  it directly, so no node needs a second upgrade.
