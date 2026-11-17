# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-17): image/video generation on own GPUs (`e6rx`) — w1 and the desktop are up, nothing is finished

- **Desktop** (RTX 5070 Ti): pinned ComfyUI stack in a uv venv on
  `~/disks/1TB-2024/ai` (`k8s/apps/comfyui/{versions.env,
  requirements.lock,setup-local.sh,run.sh}`). First numbers, Krea 2
  Turbo nvfp4, 1024², 8 steps: 8.1 s warm (0.89 s/it), ~42 s cold —
  the cold cost is the SATA SSD read, not ComfyUI (API up in 5 s).
- **w1** (RTX 3070 Mobile): on the new `-nvidia` installer variant,
  `nvidia.com/gpu: 1`, `ai/comfyui-w1` running from a `longhorn-local`
  strict-local volume with **no image of ours** (stock uv image +
  ConfigMap; ADR-0049 **Proposed**). Not benchmarked yet — its models
  were still downloading.
- **Incident, closed**: the first `-nvidia` build pulled official
  extensions by tag; Sidero had repointed three of them, w1 booted with
  `ext-iscsid` crash-looping (Longhorn down on w1 ~10 min, replicas
  degraded, nothing lost). `talosctl rollback`, then every extension
  pinned `tag@digest` and `build.sh` refuses bare tags (`24c6a17`).
- `bench.py` (same three graphs against any ComfyUI URL) and
  `scripts/gpu-handoff.sh` (w2 card vfio-pci ↔ host; **untested past
  `status`**) are written.

## Loose threads

- **w2 is untouched and needs the owner's go**: upgrading it reboots
  the node and bounces win2k25; benchmarking it needs the VM off. Then:
  add the nvidia modules after the two vfio lines in
  `asus-tuf-b550-pro.yaml`, repin, upgrade, `gpu-handoff.sh host`.
- **Undecided (ADR-0049 "Open")**: the declarative shape of ComfyUI on
  w2 while Windows may hold the card — a Pending pod pages, `replicas`
  as live state is a second invariant-2 carve-out, or `vmpower` owns
  the hand-off.
- Downloads in flight (notes 2026-11-17); the benchmark table is only
  meaningful once tier 1 is on every machine. H3 Image and H3 video
  graphs in `bench.py` are **unrun** — expect a wrong input name or two.
- `longhorn-local` read speed is unmeasured (the fallback is a raw
  Talos user volume). `ERROR: init 250 result=11` when `uv pip sync`
  runs in the GPU container is unexplained. Warm pod restart on w1:
  scheduled → Ready in 31 s.
- `comfy-w1.gw.mesh.internal` Ingress is declared but never opened in a
  browser.
- Carried from 2026-11-08: phone APK rebuild + Syncthing pairing; wipe
  the four old disks; glossary "Facet"/"Recipe" vs ADR-0048;
  `policy.Default` on devices; `oh3l`; ADR-0001 (tasks) Proposed; PR 10
  `ready → done`; `f0jl` first stop→start; `9io` old plaintext PVs.

## Suggested next steps

- Run `bench.py` against the desktop and w1 (port-forward
  `svc/comfyui-w1 8188`) once the fetches finish; fix the H3 graphs.
- Get the go for w2, then `e6rx.4`; seed w2's volume from w1 over the
  LAN.
- Owner reads ADR-0049 and rules on the w2 shape.
