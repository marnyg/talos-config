# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): H3 fused W4A8 + full 50-block measurement

- `l5km` fused packed W4A8 decode directly into B registers for signed-INT8
  m16n8k32 PTX MMA. The full real projection is bit-exact at every intermediate
  and runs in 0.27 ms (was 0.80 ms; Comfy 0.183 ms), with no decoded-weight
  workspace. A scaled BF16 epilogue avoids global INT32 projection outputs.
- `qobn` ran all 50 blocks and all six PDD steps at 64 text + 256 image tokens:
  0.316–0.325 s warm against Comfy's 102.5 ms; 768 MiB of MAX's 12.75 GiB pool
  remains after setup. Full-forward cosine is 0.993635; per-step cosine is
  0.992852–0.999403. A 384-token reference-image case passes at 0.999968.
- The 2048²-equivalent graph (4096 image + 256 text tokens) compiles but its
  first forward requests **18.76 GB for one allocation** and OOMs. No full-res
  timing exists; a quiet desktop is insufficient. `3yur` tracks the new memory
  redesign spike. Reproduction and all numbers are in
  `k8s/apps/comfyui/max-h3/README.md`.

## Same day, another session: the protocol network is live (`k7sd` line)

- ADR-0051 and protocol ADR-0012 Accepted. Fly app `marnyg-sap-net`
  (`actors/net/`) runs the lighthouse with the relay behind its
  admission gate; lighthouse `ed:0db45a…1615`. The sap plane moved onto
  it (`ssau`), `sap-lighthouse` retired (PV kept); the first child was
  born through the gate and settled (session `4d42d6b6`).
- The hub relay's member gate **enforces** (`e7f0`, `56e1c9b`,
  `RELAY_GATE=enforce`, unsealed 18:50Z): a stranger key was refused
  live, no member was. If `fly logs -a marnyg-talos-config` ever shows
  `relay access: denied` naming a member: `observe` + deploy + unseal.
- The `actors/parent` test edits the H3 session saw were this session's
  flake fixes, committed in `67b2694`.
- Loose: `w0ur` (relay/publish links do not feed the clock mark);
  ADR-0012's undecided four (signed id claim, stranger door at the
  relay, onward delegation, byte limit). Details:
  `technical/deployed-state.md`.

## Loose threads

- `l5km` and `qobn` are implemented and ready for owner review/closure; `vnb4`
  remains the parent spike. The small full model is ~3.1× slower than Comfy and
  has accumulated numerical drift, so this is not a production replacement.
- Current kernels require M/N divisible by 64 and K by 256, with no tails.
  Execution results must be released before the next forward to permit workspace
  reuse; `parity.py` does so.
- `3yur` must root-cause the 18.76 GB request before choosing chunking,
  offload, or graph restructuring. No Qwen3-VL, VAE, iterative sampler, video,
  ref2va, LoRA, ControlNet or offload exists here.
- Unrelated local edits in `actors/parent/engine_test.go` and
  `actors/parent/parent_test.go` were present and deliberately untouched.

## Suggested next steps

1. Review/close `l5km` and `qobn`; keep ComfyUI as the complete-pipeline tool.
2. Only if MAX remains worth pursuing, start `3yur` with source-traced allocation
   profiling at increasing token counts before designing 2048² execution.
3. Add aligned-shape/tail handling before considering real text lengths.
