# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session (2026-11-28): the protocol network as its own surface (`k7sd`, `qx5c`)

- Ruled by the owner, all Accepted: invariant 5 scoped to the talos
  deployment (two dependency rules), the goal "the hub is one consumer
  among many", **ADR-0051** (protocol network = its own fly app:
  `iroh-relay` + `cmd/lighthouse`; the hub keeps its relay for
  members), **protocol ADR-0012** (relay admission = a `relay` chain
  the actor presents; a newborn presents a wildcard link from its
  intro). `qx5c` closed, decision `9f7n`.
- Built, all on `main`, tested, **not deployed and unused by any
  manifest** (the running system is unchanged):
  - `actors/relaygate` (`bal3`): the gate — verify, the `id → exp`
    table, the relay's access hook, the proxy that ends a connection
    at expiry; client side `Prepare`/`Post`/`Presenter`/`Claim`.
  - `protocol/spawn` (`bpha`): `Spawner.Relay` puts a relay birth
    link in the intro and a `P→C relay` chain in the kit.
  - the child presents (`0ken`): `cmd/child` shows the gate its
    intro's link before it binds and claims its id; `child.Run`
    presents the kit's chain at birth and every beat
    (`Options.Present`); `cmd/parent`, `cmd/spawn` take `-relay-id`.
- Evidence: `TestBornThroughTheGate` (`actors/relaygate`, tagged) runs
  spawner → provisioner → child on the real relay behind the gate.
  `scripts/test-iroh.sh actors`; `actors-bin` runs it in the sandbox.

## Also this cycle (2026-11-28): Krea 2 MAX/Mojo — full model runs (`j1kd`)

- Cut the Krea 2 DiT activation footprint ~40% with two changes that keep
  correctness (BF16 RoPE; BF16 tensor-core output staged through an FP32
  shared tile). The fused kernel is now **bit-exact** against its reference.
- **The full 28-block DiT at 1024² executes**: **6.41 s per denoising step**
  (warm, RTX 5070 Ti, `fp8_mojo_tc`). ComfyUI does the same step in
  **0.95 s/it** (`fp8_scaled`) / **0.64 s/it** (`int8_convrot`), so the port
  is **~6.7× slower** end to end — the spike's headline result.
- Measured root cause of the OOM chase: **MAX's device pool equals free VRAM
  at `Accelerator` creation**, and the 12.24 GiB of FP8 weights are **fully
  resident** — MAX 26.7 has no weight offload (only LLM KV-cache tiering),
  VMM `reclaimable=0`. 1024² needs the desktop's GPU footprint under ~1 GiB;
  ComfyUI fits the same checkpoint by offloading.

## Loose threads

- The gate's rollback floor is its start, not the last presentation:
  `relay`/`publish` chain links do not feed the clock mark (`w0ur`,
  protocol invariant 9's letter). Affects the lighthouse too.
- The id claim in the relay bearer is unsigned (ADR-0012, not
  decided); also open there: a stranger door at the relay, a child
  delegating onward, a per-client byte limit.
- The caller connects to the foreign relay, so an enforcing hub gate
  blocks actor → member dials. No such edge exists today.
- `boot.ViaRelay` uses the actor's own `-relay` as the lighthouse dial
  hint; fine only while every actor shares the lighthouse's relay.
- Every relay test ran on loopback, not through fly's TLS proxy.
- Krea 2 / Mojo (`j1kd`, committed `98e39cb`): **no 28-block / 1024²
  numerical reference** — `ref_dump.py` runs ComfyUI FP32 on CPU (hours at
  that size); correctness rests on two-block parity (cosine 0.999753) and
  bit-exact per-op tests. The port is the DiT only (no sampler, text encoder
  or VAE), so 6.41 s is one step, not an image.
- **Desktop ComfyUI is stopped** (killed for VRAM) and is not a service;
  restart with `k8s/apps/comfyui/run.sh`.
- Carried over: ADR-0050 (any-port TCP) is still Proposed; the unused
  `longhorn-local` claims await the owner (`e6rx.8`).

## Suggested next steps

- `gsps`: the binary that runs gate + relay + lighthouse (relay
  supervision; `-member` mints the relay consent, delegable for
  parents).
- Then `9nkh` (the fly app), `ssau` (move the sap plane: members bind
  with the claim, manifests get `-relay-id`), `e7f0` (enforce the
  hub's member gate).
- `j1kd`: the spike has answered its question — MAX/Mojo runs the full
  Krea 2 DiT correctly but ~6.7× slower than ComfyUI and cannot offload,
  so ComfyUI stays the tool. Close it, or continue only via NVFP4 weights
  (`krea2_turbo_nvfp4`, 7.15 GiB), the one route that removes the
  desktop-VRAM negotiation.
