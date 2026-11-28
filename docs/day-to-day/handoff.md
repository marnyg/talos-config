# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): clean H3 desktop benchmarks (`e6rx.7`)

- Verified both cluster ComfyUI pods and their APIs: ComfyUI 0.45.5,
  torch 2.14.1+cu130, and the complete 15-file / 134 GB tier-1 model
  set on each node's `local-nvme` volume. Started the installed desktop
  server with `k8s/apps/comfyui/run.sh`; it is still a foreground-style
  user process, not a login service.
- Reran H3 Image, t2v and i2v on a quiet RTX 5070 Ti (52 GiB RAM
  available, ~3.2 GiB VRAM initially occupied, no meaningful swap).
  Warm totals are 6.3–6.6 s, 23.1–23.5 s and 25.8–26.6 s respectively;
  raw runs are in `bench-results/desktop-5070ti@quiet.jsonl`, and the
  comparison in `docs/technical/guides/comfyui.md` now uses them.
- Desktop cold H3 runs still take 122–139 s from the SATA SSD. The
  whole 265 GB `~/disks/1TB-2024/ai` tree cannot move to the NVMe root:
  only 98 GiB is free there. No files were moved.

## Loose threads

- The untracked `k8s/apps/comfyui/max-krea2/` tree is the separate
  Modular MAX spike (`j1kd`); it was not touched by the benchmark work.
- The clean desktop rerun covered default `w4a8`/`w6a8` only. Desktop
  `int8_convrot`, the 4-step LoRA, H3 ref-to-video and image edit still
  need clean measurements. w1 remains firmware-limited to ~50 W.
- Unused `longhorn-local` claims `ai/comfyui-w1` and `ai/comfyui-w2`
  (~85 GB each) await the owner's delete/keep decision (`e6rx.8`).
- ADR-0050 (any-port TCP) is still Proposed. Its live desktop exposure
  now includes this ComfyUI listener on `127.0.0.1:8188` to `admins`.

## Suggested next steps

- Use `http://127.0.0.1:8188`, `http://comfy-w2.gw.mesh.internal`, or
  `http://comfy-w1.gw.mesh.internal`; the stock Krea 2 / MiniMax H3
  templates and fastest known files are named in the ComfyUI guide.
- If desktop cold starts matter, free NVMe space or move only a bounded
  hot-model set; do not try to move the full tree into the current 98
  GiB free. Otherwise spend benchmark time on the unmeasured variants.
