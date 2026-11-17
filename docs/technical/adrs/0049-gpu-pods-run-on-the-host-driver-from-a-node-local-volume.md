# ADR-0049: GPU pods run on the host NVIDIA driver, from a node-local volume, with no image of ours

- Status: **Proposed** 2026-11-17 (written by the agent during epic
  `talos-config-e6rx`; the owner chose the direction — "A and B", "only
  the GPU nodes" — but has not read this text). Live on w1; w2 pending.
- Date: 2026-11-17
- Related: epic `talos-config-e6rx`; spike `talos-config-dsuj` (the
  VM↔AI-pod hand-off it left open); ADR-0023 (own install image —
  amended here: extension refs are digest-pinned); ADR-0004 (the
  re-downloadable + public test); ADR-0011 (Longhorn); invariant 2
  (live-state carve-outs are argued, not copied).

## Context and Problem Statement

The owner wants image and video generation (Krea 2, MiniMax H3 Image,
MiniMax H3 video) on his own GPUs, compared across them: the desktop
(RTX 5070 Ti, 16 GB), w1 (RTX 3070 Mobile, 8 GB, idle until now) and
w2 (RTX 3070, 8 GB, on `vfio-pci` for the win2k25 VM). Stated
priorities: speed first, a light container second, models persisted on
the cluster so they are local and fast.

No Talos node could run a GPU container: the fleet image carried no
NVIDIA driver. Four things had to be decided: how a pod gets a card,
what w2's card does while Windows wants it, where ~80–270 GB of
weights live, and what the container is.

## Decision Drivers

- Speed of the workload, then of a cold start (weights are 8–40 GB per
  model; the disk read is the cold start).
- The WAN is the scarce resource here (~13–17 MB/s measured, shared by
  every node and the desktop): anything pulled twice is paid twice.
- A node reinstall may forget anything re-derivable (invariant 2's
  corollary) — weights and wheels qualify.
- The fleet image is a boot-path artifact (ADR-0023); the installer
  package on ghcr is public.

## Considered Options

### How a pod gets a card

- **Host driver + device plugin (chosen).** Sidero's NVIDIA extensions
  in the install image, `nvidia` RuntimeClass, `nvidia.com/gpu`.
- GPU in a Linux KubeVirt VM on the existing vfio passthrough: no
  image change, hand-off is "which VM runs" — but the workload is a
  VM, its disk is virtio, and it is the opposite of a light container.
- kata + VFIO: the sandbox runtime exists (ADR-0035) but GPU
  passthrough into kata is a second research project.

### Which nodes carry the driver

- **A second installer variant, GPU nodes only (chosen by the owner).**
  `VARIANT=nvidia build.sh` appends `NVIDIA=(…)` from `installer.env`.
- One fleet image with the extensions everywhere: keeps "one image",
  costs a reboot of cp1 and nas1 for nothing they use.

### Where the weights and the Python stack live

- **A `longhorn-local` volume per GPU node (chosen):** one replica,
  `strict-local`, unencrypted, not backed up, `WaitForFirstConsumer`.
- The default class (2 replicas, encrypted, backed up to Tigris):
  replicates and uploads 100+ GB of public files.
- hostPath / a Talos user volume: fastest possible, but a new disk
  layout per node and outside Longhorn's accounting. Kept as the
  fallback if the measured read speed disappoints.

### What the container is

- **The stock `uv` + Python image, converging the volume at start
  (chosen).** ComfyUI checkout, venv (torch + CUDA wheels, ~7 GB) and
  models are on the volume; `entrypoint.sh` brings it to
  `versions.env` + `requirements.lock`, shipped as a hash-named
  ConfigMap from the app directory.
- A baked image: ~7 GB pulled over the WAN per node per bump, and a
  new ghcr package to publish. Immutable, but the only thing it buys
  over a lock file is independence from PyPI at start — and a warm
  start here touches no network.

## Decision Outcome

1. GPU nodes run the `-nvidia` installer variant: **open** kernel
   modules (the image is public; both cards are Ampere) on the **lts**
   branch (580 — what CUDA 13 / the cu130 wheels require; `production`
   is 570). Modules are loaded per machine in the hardware layer.
2. **Every official extension ref is pinned `tag@digest`** from the
   Image Factory's list for the Talos version, and `build.sh` refuses a
   bare tag. This amends ADR-0023, which pinned only the *output*
   digest: Sidero rebuilds extensions per Talos release under the same
   tag, and the first `-nvidia` build picked up an `iscsi-tools:v0.2.0`
   linked against `libcrypto.so.4` — w1 booted with `ext-iscsid`
   crash-looping and Longhorn down on the node until
   `talosctl rollback` (~10 min, replicas degraded, no data-path
   outage).
3. A pod gets a card with `runtimeClassName: nvidia` +
   `nvidia.com/gpu: 1` (`k8s/apps/nvidia-device-plugin`, vendored
   static manifest, scheduled by the extension's own node label).
4. Weights and the Python stack live on `longhorn-local`, one volume
   per GPU node; the container is the stock uv image.
5. ComfyUI is the runtime on all three machines, from one set of pins
   (`k8s/apps/comfyui`), so `bench.py` compares GPUs and nothing else.

### Open: w2's card has two owners

`scripts/gpu-handoff.sh` rebinds the card between `vfio-pci` (boot
default, the VM) and `nvidia` (pods). What is **not** decided is the
declarative shape of the w2 ComfyUI workload: a Deployment that is
Pending whenever Windows holds the card (simple, but a Pending pod
pages), `replicas` as live state ignored by ArgoCD (a second
invariant-2 carve-out after `vmpower`'s `runStrategy` — to be argued),
or `vmpower` owning the hand-off (VM stop → card to the host). Owner's
call; nothing for w2 is in git beyond the script.

### Consequences

- Good: a GPU pod is two lines of pod spec; a version bump moves only
  the wheels that changed; no registry package of ours; a wiped GPU
  node refills itself.
- Good: the digest pins close a hole that had been open since
  ADR-0023 — any rebuild of the fleet image could have done to cp1
  what this one did to w1.
- Bad: the fleet now runs two installer images; a Talos bump rebuilds
  both and re-reads every digest.
- Bad: the pod is not hermetic — a *cold* volume needs PyPI, GitHub and
  Hugging Face at start. Accepted: it is a workstation workload, not
  part of the provisioning or recovery path (invariant 4 untouched).
- Bad: `strict-local` pins the volume to its node; draining the node
  stops the workload instead of moving it. Intended — the card does
  not move either.
- Unknown: one line, `ERROR: init 250 result=11`, precedes uv's output
  when `uv pip sync` runs in the GPU container (cold volume or a lock
  bump; a warm start does not print it). Harmless so far, unexplained.
