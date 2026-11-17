# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-17): image/video generation on own GPUs (`e6rx`) — all three GPUs run ComfyUI; the H3 half of the benchmark is still downloading

- **Three ComfyUI instances from one set of pins** (`k8s/apps/comfyui`:
  `versions.env`, `requirements.lock`): the desktop (RTX 5070 Ti, uv
  venv on `~/disks/1TB-2024/ai`), `ai/comfyui-w1` (RTX 3070 Mobile) and
  `ai/comfyui-w2` (RTX 3070). The pods are the stock uv image + a
  ConfigMap converging a 300Gi `longhorn-local` volume — no image of
  ours. ADR-0049 **Accepted**.
- **w1 and w2 run the `-nvidia` installer variant**; both cards are on
  the host driver (`nvidia.com/gpu: 1` each). **win2k25 is powered off
  by the owner's ruling** and w2's card is the host's at boot;
  `scripts/gpu-handoff.sh` is the way back (`host` tested, `vm` not).
- **Incident, closed**: the first `-nvidia` build pulled official
  extensions by tag; Sidero had repointed three, w1 booted with
  `ext-iscsid` crash-looping (~10 min, replicas degraded, nothing
  lost). Rolled back; every extension is now pinned `tag@digest` and
  `build.sh` refuses bare tags.
- `bench.py` (same graphs against any ComfyUI URL; results in
  `k8s/apps/comfyui/bench-results/*.jsonl`, `--table` prints them) and
  `fetch_models.py` (resumable, sha256-checked, peer-mirroring model
  fetch) are written and in use.

## Loose threads

- **Benchmark coverage**: see `bench.py --table`. Krea 2 Turbo nvfp4,
  1024², 8 steps, warm: desktop 8.1 s (0.89 s/it, with ~6.5 GB of the
  card held by the desktop session), w1 48.5 s (5.2 s/it). **w2 is
  unmeasured** — its venv and Krea files were still arriving. Untried
  on the 3070s: `fp8_scaled`/`int8_convrot`, which may beat nvfp4 on
  Ampere. `h3_image` and `h3_t2v` graphs are **unrun everywhere**
  (the 16 GB text encoder and the H3 weights were still crossing the
  WAN) — expect to fix an input name or two on first contact, and to
  find out whether 8 GB cards run H3 at all inside the pods' memory
  limits (w1 26Gi, w2 40Gi).
- **Do not edit a file in the comfyui ConfigMap while fetches run
  unless it is worth a pod roll**: every roll restarts both containers.
  Hub pulls resume; **peer-mirror pulls do not** (`python -m
  http.server` ignores Range) and an in-flight uv wheel is lost. A
  Range-capable server in `fetch_models.py` is the fix.
- Fetches in flight (notes 2026-11-17). The desktop fetch must be
  restarted by hand after a reboot; the pods resume by themselves.
- `longhorn-local` read speed is unmeasured (fallback: a raw Talos user
  volume). `ERROR: init 250 result=11` when `uv pip sync` runs in the
  GPU container is unexplained.
- `comfy-w1.gw.mesh.internal` answers through the gateway (API checked,
  UI not opened in a browser); `comfy-w2.gw` unchecked.
- win2k25 is Halted, not deleted: its disks and manifest stand. w2's
  `deployed-state.md` passthrough paragraphs describe the old state.
- Carried from 2026-11-08: phone APK rebuild + Syncthing pairing; wipe
  the four old disks; glossary "Facet"/"Recipe" vs ADR-0048;
  `policy.Default` on devices; `oh3l`; ADR-0001 (tasks) Proposed; PR 10
  `ready → done`; `f0jl` first stop→start; `9io` old plaintext PVs.

## Suggested next steps

- When tier 1 is on all three (`kubectl -n ai logs deploy/comfyui-w1 -c
  models | tail`): `bench.py --url … --label …` for the desktop,
  `http://comfy-w1.gw.mesh.internal` and `http://comfy-w2.gw.mesh.internal`;
  then pick per-GPU variants (tier 2) from the numbers.
- Measure a cold load off `longhorn-local` vs the node's raw NVMe.
- Decide win2k25's fate (keep Halted / delete) and prune
  `deployed-state.md` accordingly.
