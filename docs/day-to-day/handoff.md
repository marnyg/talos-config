# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-10-07/08: the kagent trial was reopened before it deployed and
**replaced by a sandbox layer** — ADR-0035 Accepted, spike `bog2`,
decision `6d0u`.

- Owner's actual requirement was *sandboxed* agent workloads. kagent
  0.10.3's `Agent` cannot select a `runtimeClassName` and its sandbox
  story is Agent Substrate (k8s ≥ 1.37), so the trial drafts were
  removed (`297d0f4`); the memo `docs/spikes/agents-kagent.md` carries
  a superseded note. gVisor (wrong workload profile), Firecracker (not
  in Talos's extension, no gain at this lifetime) and KubeVirt-per-
  agent (too heavy) were weighed in the ADR.
- **Kata Containers 3.26.0 (cloud-hypervisor)** is in the fleet
  installer: `installer.env` gained the extension and a `SUFFIX`;
  `build.sh --installer-only` rebuilds without a nodeagent binary. All
  three hardware files pin `v1.12.6-p0agent-0.1.6-kata@6e8e77…`; all
  three nodes were upgraded (w1 → nas1 → cp1) and run it.
- `k8s/apps/sandbox/runtimeclass.yaml`: `RuntimeClass kata`,
  `podFixed` 200Mi/250m from measurement (`3a2b178`). A `restricted`-
  PSS alpine pod ran under it on every node (guest kernel 6.18.5).
- Bench on w1 (bog2 notes): start ~1 s cached; host RSS ≈ 200 MB idle;
  compute native; Longhorn PVC fine via virtio-fs; fork+exec 3.6×,
  small-file metadata 7–10× slower than runc.
- `4te` (parents' TV) closed as no longer needed.

## Loose threads

- `bog2` is still open — all scope items done; owner to close.
- Rolling the fleet: nas1's drain sat on Longhorn's
  `block-if-contains-last-replica` (single-replica media is nas1's by
  design) — `node-drain-policy` was flipped to `always-allow` by hand
  and **restored** to the chart default; cp1's drain sat on KubeVirt's
  infra PDBs (`bhui`). Both proceed at Talos's 5-min `DrainTimeout`
  anyway (notes 2026-10-01). 2-replica volumes rebuilt after cp1 came
  back; cdi/csi-provisioner crashlooped through the API blip and
  recovered. Pod corpses from the evictions were deleted.
- `kata-qemu` handler is on the nodes, not declared in git.
- Standing: herdr 0.9.1 vs 0.8.2, `jlgz`, `bsj`; two `<!-- stale? -->`
  flags in `notes.md` (Quint entries) still await the owner.

## Suggested next steps

- `tj7c`: the first agent under kata — one Job-shaped harness image,
  OpenRouter key sealed into `ai`, wallet-gated entry; also the first
  agent-as-child candidate.
- `5h0j` only if that agent's workspace feels the virtio-fs cost.
- `bsj` backup target; `dsuj` still waits on the Windows data copy.
