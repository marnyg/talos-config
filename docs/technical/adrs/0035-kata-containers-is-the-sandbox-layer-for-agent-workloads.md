# ADR-0035: Kata Containers (cloud-hypervisor) is the sandbox layer for agent workloads

- Status: Accepted — in effect 2026-10-08: all three nodes boot
  `talos-installer:v1.12.6-p0agent-0.1.6-kata`, `RuntimeClass kata` is
  applied, and a `restricted`-PSS pod ran under it on every node
  (guest kernel 6.18.5). Decision `talos-config-6d0u`, spike `bog2`.
- Date: 2026-10-07
- Related: ADR-0023 (the owned install image — this is its second
  extension), ADR-0011/0029 (Longhorn; volumes reach the guest over
  virtio-fs), spike `talos-config-kanr` + `docs/spikes/agents-kagent.md`
  (the orchestrator question, now reopened as `tj7c`), `dsuj` (KubeVirt
  as the heavy alternative), protocol ADR-0009 (children; the sandbox
  is where an agent-as-child would run)

## Context and Problem Statement

The owner wants agentic workloads on the cluster — agents that run
arbitrary code (shells, package managers, builds) against a workspace
— and wants them *properly sandboxed*. Spike `kanr` had evaluated
orchestrators (kagent 0.10.3, google/ax) and recommended a time-boxed
kagent trial; that ruling treated isolation as a later upgrade (Agent
Substrate, k8s ≥ 1.37). Reopening it with sandboxing as the primary
requirement inverted the dependency: isolation is a property of the
pod's runtime, not of the orchestrator, and nothing in the fleet
provided one. There was no `RuntimeClass`; the only commented hint
was the deprecated `machine.install.extensions` gvisor block in
`talos/base/*.yaml`.

## Decision Drivers

- The workload profile: fork/exec-heavy, syscall-heavy, long-lived
  (minutes to hours), not millisecond functions.
- No cluster upgrade: k8s 1.32.3 / Talos 1.12.6 stays; the sandbox
  must come from what Talos ships today.
- Invariant 2: the runtime must be declared in git (installer image
  pinned by digest, ADR-0023; the `RuntimeClass` a manifest).
- A real kernel boundary, not a syscall filter — prompt-injected
  agents will probe the container from inside.
- Headroom on nas1 (4 × 800 MHz, 7.5 GiB): the per-sandbox fixed cost
  must be accounted for at scheduling.
- Keep the orchestrator decision open: whatever runs the agent later
  (bare Job, kagent 1.0, ax) must be able to select the sandbox.

## Considered Options

### Option A: gVisor (`runsc`)
Official Talos extension; user-space kernel, ~20 MiB fixed.
- Pros: lightest, fastest start, no KVM needed.
- Cons: syscall interposition is slowest exactly on this workload
  (fork/exec, metadata-heavy file I/O); known compatibility gaps bite
  developer tooling; still shares the host kernel.

### Option B: Kata Containers with cloud-hypervisor (chosen)
Official Talos extension `kata-containers:3.26.0`, handlers `kata`
(cloud-hypervisor) and `kata-qemu`. Each pod boots its own guest
kernel under KVM; rootfs and volumes are shared via virtio-fs.
- Pros: hardware boundary; native compute; sub-second start; one
  line in `installer.env` + one `RuntimeClass`; stock extension, no
  custom build; KVM already verified on the nodes (KubeVirt).
- Cons: ~200 MB host RSS per sandbox (measured, above upstream's
  130 Mi figure); exec/metadata-heavy work pays 3–10× through
  virtio-fs with the shipped config (`cache=auto`, no DAX), which the
  extension ships read-only (`5h0j`); cannot share hostNetwork/PID or
  run privileged — a feature here.

### Option C: Kata with Firecracker
Not in Talos's extension (it builds cloud-hypervisor and QEMU only);
would need a custom extension plus the devicemapper snapshotter.
- Ruled out: its edge is boot latency and density for ms-scale
  functions; same VMM class as cloud-hypervisor, fewer devices (no
  virtio-fs), nothing gained at this sandbox lifetime.

### Option D: KubeVirt VM per agent
Already installed (`k8s/apps/kubevirt`, `vms/`).
- Pros: strongest isolation, full distro, persistent state.
- Cons: 10–60 s boot, 0.5–1 GiB+ per VM, `virt-launcher` overhead;
  wrong grain for per-task sandboxes. Kept as the heavy fallback for a
  long-lived agent box.

### Option E: Agent Substrate (kagent 1.0 / ax)
gVisor/microVM sandboxes bundled with an orchestrator.
- Ruled out for now: needs k8s ≥ 1.37 and a Talos bump, kagent 1.0 is
  alpha with no in-place upgrade, ax has no OpenRouter and is not
  GitOps-owned. Re-evaluate once 1.0 is GA — from a cluster that
  already has a sandbox layer, so the question is purely whether their
  orchestrator is wanted.

## Decision Outcome

Chosen: **Option B.** The kata-containers extension joins the
`OFFICIAL` list in `talos/extensions/installer.env`; `build.sh
--installer-only` rebuilds the installer from the extension already in
the registry and the installer tag carries a `SUFFIX` so a changed
extension list never shares a tag with its predecessor. The
`RuntimeClass kata` lives in `k8s/apps/sandbox/runtimeclass.yaml` with
`overhead.podFixed` set from measurement (200 Mi / 250 m), so the
scheduler charges the sandbox to the node. Only the cloud-hypervisor
handler is declared; `kata-qemu` is on the nodes if GPU passthrough or
nested virt is ever wanted.

The kagent 0.10.3 trial is dropped before deployment (drafts removed;
memo kept with a superseded note). The orchestrator is reopened as the
null option under kata (`tj7c`): a Job-shaped harness image with
`runtimeClassName: kata`, which is also the first agent-as-child
candidate — a Job with a deadline is already how the k8s driver births
children.

### Consequences

- Any pod may opt into a guest kernel with one line; nothing else
  changes. Pods without the field are unaffected.
- The fleet installer now carries a third-party-free but non-trivial
  runtime; a Talos bump means checking the extension's version for
  that Talos first (the factory lists it per version).
- Measured on w1 (bog2 notes): start ~1 s with a cached image; host RSS
  ≈ 200 MB idle; compute native; bulk sequential I/O ~1× (Longhorn-
  bound); fork+exec 3.6×, small-file metadata 7–10× slower. Agent
  workspaces should expect that until `5h0j` tunes virtio-fs or moves
  the workspace onto a block device.
- Budget remains the open gap: nothing in-cluster meters an agent's
  spend; the OpenRouter key's own limit is the bound until M5 counted
  caveats (`0bc.5`).
- Rolling the fleet surfaced two drain blockers worth knowing: nas1's
  single-replica media volumes trip Longhorn's
  `block-if-contains-last-replica`, and KubeVirt's infra PDBs pin to
  the single control plane (`bhui`); Talos's 5-minute `DrainTimeout`
  proceeds regardless.

## Confirmation

`talosctl get extensions` on each node lists `kata-containers 3.26.0`;
`kubectl get runtimeclass kata` exists; a `restricted`-PSS pod with
`runtimeClassName: kata` reports a guest kernel distinct from the host's
(`uname -r` 6.18.5 vs host 6.18.18). Invalidated if an agent workload
proves unusable through virtio-fs (then `5h0j` first, KubeVirt second),
or if the Image Factory gains kata by default and ADR-0023's image is
no longer needed for it.
