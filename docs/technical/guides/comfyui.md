# ComfyUI on own GPUs — how to use it, what it measured

Epic `talos-config-e6rx`, ADR-0049. Three ComfyUI instances run the
same pinned stack (`k8s/apps/comfyui/versions.env` +
`requirements.lock`: ComfyUI v0.45.5, torch 2.14.1+cu130):

| Where | GPU | URL | Runs from |
|---|---|---|---|
| desktop | RTX 5070 Ti 16 GB | `http://127.0.0.1:8188` | uv venv + models on `~/disks/1TB-2024/ai` (SATA SSD) |
| w2 | RTX 3070 8 GB | `http://comfy-w2.gw.mesh.internal` | `ai/comfyui-w2`, `local-nvme` volume |
| w1 | RTX 3070 Mobile 8 GB | `http://comfy-w1.gw.mesh.internal` | `ai/comfyui-w1`, `local-nvme` volume |

The cluster URLs are behind the admins gate like every other Ingress.
The stock templates work as shipped (Templates → Krea 2 / MiniMax H3);
pick the model files named under "Which files" below.

## Day to day

- **Desktop**: `k8s/apps/comfyui/setup-local.sh` once (idempotent),
  then `k8s/apps/comfyui/run.sh`. It is not a service — nothing starts
  it at login.
- **Models**: `k8s/apps/comfyui/models.txt` is the manifest (tier 1 =
  on every machine, tier 2 = the other variants).
  `python3 k8s/apps/comfyui/fetch_models.py 2` fills the desktop tree
  and resumes where it stopped; the pods run the same script and
  mirror each other (`MODEL_TIER` in `comfyui.yaml`).
- **A bump** (ComfyUI, torch, a model): edit `versions.env` /
  `requirements.lock` / `models.txt`, push. The ConfigMap name changes,
  the pods roll, and each converges its volume. The lock is
  `uv pip compile <ComfyUI checkout>/requirements.txt --python-version
  3.13 --python-platform x86_64-manylinux_2_28 --torch-backend cu130
  --no-header -o requirements.lock`.
- **Benchmark**: `bench.py --url <url> --label <name>`; results append
  to `k8s/apps/comfyui/bench-results/<label>.jsonl`, `bench.py --table`
  prints everything.
- **w2's card back to a VM**: scale `comfyui-w2` to 0,
  `scripts/gpu-handoff.sh vm`, start the VM; `… host` or a reboot
  returns it. `vm` has never been run.

## Which files

`int8_convrot` is the one to use for **Krea 2** on every card here —
it is also what upstream recommends on cu130:

| Krea 2 Turbo, 1024², 8 steps, warm | 5070 Ti | 3070 (w2) | 3070 Mobile (w1) |
|---|---|---|---|
| `krea2_turbo_int8_convrot` | **6.2 s** (0.64 s/it) | **13.4 s** (1.33) | **29.9 s** (3.15) |
| `krea2_turbo_nvfp4` | 8.1 s (0.90) | 26.9 s (2.88) | 48.4 s (5.26) |
| `krea2_turbo_fp8_scaled` | 8.8 s (0.95) | 26.5 s (2.90)¹ | 49.6 s (5.65)¹ |

¹ measured while the volume was still on Longhorn; warm runs do not
touch the disk.

For H3 the same family is **not** the clear win — measure before
switching. Both are in tier 1; the smaller files are the default:

| Warm run | 3070 (w2) | 3070 Mobile (w1) |
|---|---|---|
| H3 Image `pruned_w4a8` (12.6 GB), 2048², 6 steps | **15.8 s** (1.74 s/it) | **42.7 s** (4.89) |
| H3 Image `pruned_int8_convrot` (21 GB) | 16.1 s (1.68) | 49.9 s (5.04) |
| H3 video t2v `fl2va_pruned_w6a8` (16 GB), 832×480, 56 f, 6 steps | 44.4 s (5.69) | 102 s (12.6) |
| H3 video t2v `fl2va_pruned_int8_convrot` (21 GB) | **39.9 s** (4.81) | not run |

On the desktop H3 Image `int8_convrot` ran 33 s against 11–27 s for
`w4a8`, but that run was in swap (footnote 2) and says nothing about
the card.

## What the three GPUs do (warm, tier-1 files)

| Case | 5070 Ti | 3070 (w2) | 3070 Mobile (w1) |
|---|---|---|---|
| Krea 2 Turbo `int8_convrot`, 1024², 8 steps | **6.2 s** | 13.4 s | 29.9 s |
| H3 Image `w4a8`, 2048², 6 steps | **6.3–6.6 s** (0.77 s/it)² | 15.8 s (1.74) | 42.7 s (4.89) |
| H3 video t2v `w6a8`, 832×480, 56 frames, 6 steps (8-step LoRA) | **23.1–23.5 s** (2.25–2.33 s/it)² | 44.4 s (5.69) | 102 s (12.6) |
| — same, 4 steps (4-step LoRA) | 28 s³ | 32.8 s | — |
| H3 video i2v, same settings | **25.8–26.6 s** (2.57–2.62 s/it)² | 48.9 s (6.41) | 112 s (13.9) |

² **Clean desktop rerun, 2026-11-28** (`desktop-5070ti@quiet.jsonl`):
52 GiB RAM was available, ~3.2 GiB VRAM was occupied before the run,
and swap stayed at 0–1.2 MiB. These numbers supersede the 2026-11-18
H3 desktop ranges, which were measured with ~46 GB RAM and ~6.7 GB
VRAM already occupied and ended in swap.

³ The old, contended 4-step desktop run is retained only as history;
it is slower than the clean 6-step run and needs a clean rerun before
being used to choose a LoRA.

Reading it:

- **w2's desktop 3070 runs every model, H3 video included, on 8 GB.**
  The 5070 Ti is about 2.2× faster on Krea 2, 2.4× on H3 Image, and
  1.9× on the two H3 video cases.
- **w1 is 2–2.8× slower than w2 on the same chip** because the laptop
  holds its GPU to ~50 W (100 % utilisation at 46–55 W, 1065 of
  2100 MHz, throttle reasons `0x24`; w2 draws 217 W). Nothing in
  Talos can lift that; it is firmware/EC policy.
- **ComfyUI itself costs little**: ~0.1 s per run outside
  encode/sample/decode; process start to API 5 s; a warm pod is Ready
  ~30 s after scheduling. The default-on "Comfy model compiler" made
  no measurable difference either way (`--disable-comfy-compiler`).
- **The cold start is the disk.** Page-cache-cold H3 Image on w2:
  64.8 s with the volume on Longhorn, 25.7 s on the bare NVMe (warm
  15.8 s). That is why the pods run from `local-nvme`. The desktop's
  SATA SSD is the slow one now (~370 MB/s): the clean 2026-11-28 H3
  cold runs took 122–139 s even though their warm runs took only
  6–27 s. Moving the whole 265 GB desktop tree to its NVMe is not
  currently possible: the root filesystem has only 98 GiB free.

## MAX/Mojo spike

`k8s/apps/comfyui/max-krea2/` contains a separate Krea 2 DiT port
(`j1kd`) using MAX 26.7's Python graph API and custom Mojo kernels. It is
not a ComfyUI replacement or an end-to-end image pipeline. After an
optimization pass, one real Krea projection takes 36–38 ms in fused
tensor-core Mojo against 9.2–9.6 ms in ComfyUI/PyTorch, with about half
the allocation of bound-weight dequant+cuBLAS.

**The full 28-block DiT now runs at 1024²**: 6.41 s per denoising step
against ComfyUI's 0.95 s/it (`fp8_scaled`) and 0.64 s/it (`int8_convrot`)
— i.e. ~6.7× slower per step. It only fits because the desktop's GPU
footprint is under ~1 GiB: MAX's pool equals the free VRAM at startup,
the 12.24 GiB of FP8 weights are fully resident, and MAX has no weight
offload (only LLM KV-cache tiering). ComfyUI fits the same checkpoint by
offloading. Setup, correctness tests, measurements and limitations are in
`max-krea2/README.md`. Nothing in the deployed ComfyUI stack uses MAX.

## Known gaps

- `scripts/gpu-handoff.sh vm` is untested.
- The `longhorn-local` claims `ai/comfyui-w1` / `ai/comfyui-w2` are
  unused copies (~85 GB each) awaiting a delete.
- H3 reference-to-video (`ref2va`), image edit and the 27 GB
  `int8_convrot` text encoder are tier 2 and unmeasured. The clean
  desktop rerun covered the default `w4a8`/`w6a8` files only; the old
  desktop `int8_convrot` and 4-step results are still contended.
- `ERROR: init 250 result=11` as the first line of every GPU container
  start: unexplained, harmless so far.
