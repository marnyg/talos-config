# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-22 — **nas1 provisioned (node three, the storage node), and a
protocol regression fixed on the way.**

- **`talos/machines/6c-bf-b5-05-51-a8/` + `hardware/terramaster-f4-425-plus.yaml`**
  (`72f9038`): TerraMaster F4-425 Plus, worker, `diskEncryption: true`.
  Install by `diskSelector: type: nvme` (never `/dev/sda` — a 2.1GB USB
  stick); EPHEMERAL 86GB, `u-longhorn` grew to **912GB**. Longhorn's
  `create-default-disk` label is declared in `nodeLabels` — the first
  node where it is git-derived rather than `kubectl label`.
  Verified live: `Ready`, Longhorn node schedulable, KMS sealed *and*
  unsealed under the declared UUID, member `ed:90cf67ec…` minted on the
  machine, `beat ok`. Details in `technical/deployed-state.md`.
- **Protocol bug `ydq0`, found by the deploy** (`32ef88c`, hashes
  `4518c2f`): the hub image builds its test suite, and M3 (`5c2bf7d`)
  had broken `TestHubBeatOverIroh` + `TestNodeAgentEndToEnd` three
  commits back — `main` was undeployable and nobody knew. `Send`
  dropped a held chain's first link whenever the receiver signed it,
  assuming receiver-signed ⇒ the receiver's own consent; the hub's beat
  grant is receiver-signed *and* a link (its hot key signs as the
  wallet, ADR-0018), so every node's chain was emptied and answered
  `ErrAudUnbound`. Moved receiver-side into `VerifyChain.chainUnder`.
  Protocol handoff has the detail.
- **Hub `4518c2f` deployed**, unsealed, nas1 approved.

## Loose threads

- ~~Quint own-consent strip~~ — ported in `08efe79` (`chainUnder`,
  `links()`, witness `ownConsentPresentedTest`, mutation-tested); model
  and Go agree again. The standing rule (model before Go) was broken
  once under deploy pressure; noted, not repeated.
- `-tags iroh` tests run only inside the nix build; `go test ./...` in
  `config-server/` skips them silently — `scripts/test-iroh.sh`
  (`08efe79`) is the gate now, named in AGENTS.md.
- nas1's four SATA bays are empty — capacity is one NVMe partition.
- Longhorn StorageClass is still `defaultClassReplicaCount: 2` with
  three nodes now present (the chart comment says "revisit when node
  three lands"). Undecided on purpose.
- Carried: w1 off since 2026-09-21 (`9l67`); `*.gw` services and three
  media volumes follow it. Clients on `enrollmsg` v2 (`5q33`). TV not
  measured (`4te`). `-n cp1` by name fails from the tun (`t7b2`).

## Suggested next steps

- Populate nas1's SATA bays: one `UserVolumeConfig` per disk by serial
  + kubelet `extraMounts` + switch the Longhorn label to `config`.
  Applies live, no reinstall.
- Decide the replica count now that node three exists, and whether cp1
  and w1 should declare their Longhorn label like nas1 does.
