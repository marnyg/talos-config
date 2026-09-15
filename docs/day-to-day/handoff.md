# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-15 — **Mesh v3 P0.3 (Talos extension proof) PASSED** (`main
1a0a868 → 2aff5f1`, spike branch `spike/mesh-v3-p0.3` merged `--no-ff`;
bead `359.1.3` closed).

- `iroh-go/cmd/p0agent` (`serve` = node agent, `bridge` = desktop side)
  runs on **cp1** as system extension `ext-p0agent`: boots at uptime
  ≈ 11 s after NTP sync, homes on the scratch relay in 3 s, forwards
  ALPN `mesh/apid/v1` to apid `:50000`. `talosctl` ran end to end over
  it (mTLS intact, LAN-direct from the wired laptop). `talosctl reboot`
  → same NodeId from `/var/lib/p0agent/key`, bridge redialed in 40 ms.
  Full data `docs/mesh-v3-iroh.md §P0.3`; chain in
  `talos/extensions/p0agent/build.sh`.
- Two hard-won platform facts: the **Image Factory cannot ship a
  third-party extension** (→ imager + `ghcr.io/marnyg/{p0agent,
  talos-installer}`, and `--base-installer-image` does *not* inherit
  the factory's extensions — cp1 ran ~25 min without nebula/iscsi); an
  **extension with a `/var` mount must `depends: - service: cri`** or
  upgrade/reboot hangs closing LUKS EPHEMERAL (unblocked by hand twice).
- Phase 0 status: P0.4 ✓, P0.1 ✓, P0.3 ✓; **P0.2 Android remains**, then
  the gate `359.1.5`.

## Loose threads

- **cp1 runs an image git does not declare**: installer
  `ghcr.io/marnyg/talos-installer:v1.12.6-p0agent-0.0.3` (four
  extensions) vs `talos/hardware/minipc.yaml`'s factory `6a9acc…`.
  Knowing deviation from invariant 2, bead `5cz` (blocks the gate):
  upgrade back or make the imager chain the declared image in Phase 1.
- Scratch relay still up and open (`kql`); `ext-p0agent` dials it on
  every boot. Both ghcr packages are public.
- cp1's LAN lease moved `.42 → .58` in one day; `talos/talosconfig`
  endpoints still say `10.99.0.54` (wg0 era) — every `talosctl` needs
  `-e/-n`. The mesh route needs `nebup`; the LAN route needs the current
  lease.
- `mar@nixos:~/p0` is checked out at the spike branch (detached); it is
  the x86_64 builder for `.#p0relay-static`.
- Bridge trick for `talosctl` over iroh: dial a name that is in apid's
  cert SANs (`talos-wu6-eib`) via a hosts entry — `127.0.0.1` is not a
  SAN. The hosts line was removed at session end.

## Suggested next steps

- **`359.1.2` Android feasibility — planned 2026-09-16, not started.**
  The whole plan (path, four owner-confirmed decisions, seven
  fail-fast steps, traps) is in
  [`docs/mesh-v3-p0.2-android.md`](../mesh-v3-p0.2-android.md); start
  at step 1 (cross-compile `libiroh_ffi.a` for
  `aarch64-linux-android` on the NixOS box via `androidenv`) on branch
  `spike/mesh-v3-p0.2`. Bead is claimed / in_progress.
- Then `359.1.5` gate decision; `5cz` and `kql` ride on it.
- `xwu` (verb = root consent's verb) stays the M3 pre-work.
