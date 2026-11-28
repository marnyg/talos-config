# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): H3 three-GPU MAX vs Comfy matrix

- `y85j` verified the fused signed-INT8 PTX path on desktop 5070 Ti, w2 RTX
  3070 and w1 RTX 3070 Mobile. Ampere fused-MMA accumulators are bit-exact;
  two-block numerical cosine is ≥0.999967 on every GPU.
- Real projection MAX/Comfy: desktop 0.27/0.183 ms, w2 0.575/0.940 ms,
  w1 1.294/1.400 ms. Two-block 320-token MAX/Comfy: desktop 15/7.52 ms,
  w2 35/18.77 ms, w1 65/30.81 ms.
- Full 50-block MAX runs only on desktop (321 ms vs Comfy 102.5 ms). The
  11.69 GiB resident packed weights cannot fit either 8 GB 3070; Comfy's
  offload works there. The 2048² MAX graph requests 18.76 GB and OOMs on
  desktop (`3yur` owns that memory redesign).
- Raw results are committed in `max-h3/benchmarks/three-gpu.json`; comparison
  tables and cluster setup notes are in its README and
  `docs/technical/guides/comfyui.md`. Existing full-pipeline 2048² Comfy
  numbers remain separate from these DiT-only shapes.

## Same day, another session: the protocol is its own repo

- **github.com/marnyg/sap** (ADR-0052, superseding ADR-0020): protocol/,
  iroh-go/, iroh-transport/, actors/ (incl. the network's fly app), the
  images and the protocol's Quint models moved with history, tagged
  `*/v0.1.0`. This repo consumes it: `config-server/go.mod` requires the
  tags, the flake input `sap` supplies libiroh_ffi + iroh-relay (same
  store paths as before). The running hub is unchanged.
- The 18 open protocol beads moved to sap's database (`sap-*`, each
  names its original). sap is on Forgejo (`mar/sap`, origin) with a push
  mirror to GitHub, made with `k8s/apps/forgejo/migrate-from-github.sh`.
- Earlier the same day: the protocol network went live
  (`marnyg-sap-net`), the sap plane moved onto it, and the hub relay's
  member gate enforces (`e7f0`). Details: `technical/deployed-state.md`.

## Loose threads

- `l5km`, `qobn` and `y85j` are ready for owner review/closure. MAX remains a
  DiT-only spike, not a ComfyUI replacement. Full-model numerical drift and
  2048² memory still rule out production use.
- MAX was installed manually into `/data/max-venv` on w1/w2 for measurement;
  normal ComfyUI manifests do not deploy it. Cluster setup needs
  `HOME=/data UV_CACHE_DIR=/data/.uv-cache` because container `/` is read-only
  for the runtime user.
- Current kernels require M/N divisible by 64 and K by 256 and have no tails.
  No Qwen3-VL, VAE, iterative sampler, video or MAX offload exists.

## Suggested next steps

1. Review/close the three measured H3 tasks; use ComfyUI for actual generation.
2. Start `3yur` only if 2048² MAX is still worth pursuing: source-trace the
   18.76 GB request before choosing graph chunking or offload.
3. If MAX becomes more than a spike, make its cluster environment/benchmark
   runner declarative instead of keeping the manual pod installation.
