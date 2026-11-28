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
  - the binary (`gsps`): `cmd/lighthouse -relay-bin … -listen …` runs
    `iroh-relay` as a supervised child behind the gate, rooted at the
    lighthouse's key (`relaygate.Service`); `-relay` is then the
    public URL, `-member` also admits to the relay (delegably).
- Evidence: `TestBornThroughTheGate` (`actors/relaygate`, tagged) runs
  spawner → provisioner → child on the real relay behind the gate.
  `scripts/test-iroh.sh actors`; `actors-bin` runs it in the sandbox.

## Also this cycle (2026-11-28): Krea 2 MAX/Mojo — NVFP4 fits (`j1kd`)

- Added mixed NVFP4: native MAX W4A4 for QKV/MLP gate+up and a packed custom
  W4A16 kernel for attention gate/output/MLP down. The full 28-block DiT at
  1024² takes **3.21 s/step** (FP8 was 6.41 s) and fits **with the desktop
  active**, retaining 5.0 GiB of MAX pool after setup.
- Added real ComfyUI NVFP4 GPU references. Full-model cosine is **0.993576**
  against normal dynamic-scale output and **0.997914** against ComfyUI's
  unit-scale variant; two-block parity is 0.999766. Existing FP8 tests and
  the new multi-tile NVFP4 tests pass.
- ComfyUI remains faster (0.90 s/it NVFP4, 0.64 s/it int8_convrot) and is the
  tool to use. The MAX port is still DiT-only, not an image pipeline.

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
- Krea 2 / Mojo (`j1kd`): MAX's native NVFP4 API requires a host global
  activation scale, so the port cannot reproduce ComfyUI's computed device
  scale; the full-reference difference above is measured. There is still no
  28-block FP32/FP8 reference, and no sampler, text encoder or VAE.
- **Desktop ComfyUI is stopped** (killed for VRAM) and is not a service;
  restart with `k8s/apps/comfyui/run.sh`.
- Carried over: ADR-0050 (any-port TCP) is still Proposed; the unused
  `longhorn-local` claims await the owner (`e6rx.8`).

## Suggested next steps

- `9nkh`: the fly app — image with `iroh-relay` beside the
  lighthouse (it already ships a CA bundle, which the relay needs), a
  fly volume for the key, deploy script, Gatus on `/generate_204`.
- Then `ssau` (move the sap plane: members bind with the claim,
  manifests get `-relay`, `-lighthouse`, `-relay-id`), `e7f0` (enforce
  the hub's member gate).
- `j1kd`: close the spike. Porting and the NVFP4 continuation are answered;
  MAX is still slower than ComfyUI but NVFP4 removes the desktop-VRAM
  negotiation. Further kernel/global-scale work needs a new measured goal.
