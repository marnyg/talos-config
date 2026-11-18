# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-17/18): image/video generation on own GPUs (`e6rx`) — three GPUs, three models, measured

- **ComfyUI runs on the desktop (5070 Ti), w2 (3070) and w1 (3070
  Mobile) from one set of pins**; Krea 2, MiniMax H3 Image and H3 video
  (t2v + i2v) work on all three, 8 GB cards included. How to use it and
  every number: [`technical/guides/comfyui.md`](../technical/guides/comfyui.md);
  raw results `k8s/apps/comfyui/bench-results/`. ADR-0049 Accepted,
  amended the next day (storage).
- **Cluster side**: w1 + w2 on the `-nvidia` installer variant, cards
  on the host driver (`nvidia.com/gpu: 1` each), pods = stock uv image
  + ConfigMap converging a node-local volume. **win2k25 is Halted by
  the owner's ruling**; w2's card is the host's at boot
  (`scripts/gpu-handoff.sh` is the way back; `vm` untested).
- **Three findings that changed the setup**: (1) Longhorn read at
  350–465 MB/s vs 2.7–3.1 GB/s bare → the pods moved to static `local`
  PVs (`local-nvme`); page-cache-cold H3 Image on w2 64.8 s → 25.7 s.
  (2) `int8_convrot` is the variant to use (Krea 2: 1.4× faster than
  nvfp4 on the 5070 Ti, 2.1× on the 3070s) → now tier 1. (3) `hf
  download` does not resume → `fetch_models.py` (Range resume, sha256
  vs the hub, peer mirror).
- **Incident, closed**: the first `-nvidia` build took official
  extensions by tag; Sidero had repointed three, w1 booted with
  `ext-iscsid` crash-looping (~10 min, nothing lost). Every extension
  is now pinned `tag@digest`; `build.sh` refuses bare tags.
- Stale comments fixed on the way (w1 verify address, "all three"
  hardware files, `deployed-state.md` saying w1 is off); the
  backup-verify failure is bug `xgxd`.

## Loose threads

- **The desktop's H3 numbers are not the card's**: taken with ~46 GB
  RAM and ~6.7 GB VRAM already held by the owner's session; the last
  H3 run pushed ~25 GB of that session into swap. Re-run on a quiet
  desktop. Its models also sit on a SATA SSD (cold H3 runs 90–140 s).
- **w1's GPU is capped at ~50 W by the laptop** (notes 2026-11-18) —
  2–2.8× slower than w2. Firmware/EC, not ours to lift from Talos.
- **Awaiting the owner's delete**: the unused `longhorn-local` claims
  `ai/comfyui-w1` / `-w2` (~85 GB each) and then the `longhorn-local`
  class; win2k25 itself (Halted, disks intact); an empty
  `/var/lib/e6rx-scratch` on w1 and w2.
- Desktop tier-2 fetch may still be running or stopped
  (`python3 k8s/apps/comfyui/fetch_models.py 2` resumes it); the
  desktop ComfyUI is a hand-started process, not a service.
- Unmeasured: H3 ref-to-video and image edit, the 27 GB int8 text
  encoder, `gpu-handoff.sh vm`. Unexplained: `ERROR: init 250
  result=11` at every GPU container start.
- Carried from 2026-11-08: phone APK rebuild + Syncthing pairing; wipe
  the four old disks; glossary "Facet"/"Recipe" vs ADR-0048;
  `policy.Default` on devices; `oh3l`; ADR-0001 (tasks) Proposed; PR 10
  `ready → done`; `f0jl` first stop→start; `9io` old plaintext PVs.

## Suggested next steps

- Quiet-desktop rerun of `bench.py` (H3 cases); consider moving the
  desktop's `ai/` tree to the NVMe if cold starts matter there.
- Owner: delete or keep win2k25 and the old claims; then prune
  `deployed-state.md`'s passthrough paragraphs.
- Back to the task layer (`focus.md`).
