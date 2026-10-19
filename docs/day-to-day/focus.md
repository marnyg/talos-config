# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Build the agent backend designed in ADR-0037. Done: Forgejo
(`ivof.1`), `agent-run.sh -r/-b` (`ivof.2`), driver RuntimeClass +
outfit (`e6pu.1`), and 2026-10-12 the child as an agent (`e6pu.2`:
`protocol/facethttp`, `actors/pirpc`, `#task`/`#steer`/`#abort` with
the `agent-events` uploader, `spawn -prompt` as the stand-in parent), and 2026-10-17 the child
shipped in the `sandbox-agent` image and passed the live acceptance
under kata (provisioner `-runtime-class`/`-outfit`, `e6pu`); 2026-10-18
the git half followed (`e6pu.3`: `#task {repo, ref}` → branch + PR,
pointer on `child_settled`; `e6pu` closed); 2026-10-18 the **parent
actor** itself (`ivof.4`–`ivof.8`: `actors/parent` store + actor side +
web, `cmd/parent`, `k8s/apps/sap-parent`; the child's upload became
resumable on the way) — **live and confirmed** the same day, restart
mid-run included.
**2026-10-22: the task layer** (spike `y8m0`, ADR-0043 Proposed):
tasks above sessions, a project's flow of persona-driven states
(`agent|human|children`), one `transition` op, goals as the frame —
built, tested, **deployed 2026-10-22** (`a082862`; PVC snapshot
taken first). Next: the first real task through `framed` (`kmlg`),
then `bsj` (backup) — now
blocking, since flows and goals are policy the store alone holds.
Epic `ivof`. **Side effect:** the relay gate
(`5gz`) is observe-only until sovereign actors have an admission
story (spike `qx5c`). **Detour landed 2026-10-13:** root
rotation (protocol ADR-0010, spike `aja2`; ADR-0011, `d0o0` wires
pins into every receiver) — model + code built, trivial pins
everywhere; the durable `talos/` copy of pins/rotations is the open
remainder.

**Detour 2026-10-16, done:** the workouts tracker (`yz75`) is live and
seeded, and HTTPS over the mesh (ADR-0033, `eu0m`) landed so it
installs as a PWA; 2026-10-21 supersets + warm-up ramp on the live
screen and plates in the display unit (`ne6e`, `63fi`, swarm). What
remains is redeploying and using it (first real session on the phone). **Detour 2026-10-19, done:** Vaultwarden (`l5v8`) at
`vault.gw.mesh.internal` — the first https-only consumer of ADR-0033
beyond the PWA; first account + extension pairing remain. **Detour
2026-10-17, done:** Home Assistant (`k8s/apps/homeassistant`) at
`home.gw.mesh.internal`, which surfaced and fixed the gateway's
WebSocket bug (`o8wd`, `protocol/facethttp` read deadlines); the
discovery question (`b311`) remains. **Detour 2026-10-20, done:**
Audiobookshelf + Chaptarr (`htsj`) on a shared `books` library;
first-run UI setup on both remains. **Detour 2026-10-21, done:** Actual
Budget (`7yzv`) at `budget.gw.mesh.internal`, Vaultwarden posture;
server password + first budget remain. **Detour 2026-10-17, done:**
CI on the cluster (`0m50`, ADR-0039 Accepted): a Forgejo runner feeds
Attic with this flake and the NixOS systems; pointing the machines'
nix.conf at it remains. **Detour 2026-10-18, done:** the observability
stack recorded (ADR-0041) and pages wired to a self-hosted ntfy over
the mesh (ADR-0042, `88uq`); the phone subscription and the first
real page remain. Back to the line above.

**Toward goal:** "Sovereign-actor protocol at the center" (`goals.md`)
— the parent is the first long-lived actor that spawns children for
real work, and the first with a durable datastore of its own; it is
the N=2 consumer the protocol needs. Budget stays the open gap until
M5 (`0bc.5`).

**Out of scope:** migrating beads into the task layer (`hp79` first);
per-project credentials; `tools[]` on `#task`; kagent/ax (decision `6d0u`); volumes on the lease
(`264f`, until a spawned consumer); Forgejo deploy keys per child (the
per-repo endgame); SSH to Forgejo; a terminal client of the parent's
stream (after the web UI); NetworkPolicy for the sandbox; a second
control plane (`bhui`, `dsuj`); KMS onto 443 (`os8s`).
