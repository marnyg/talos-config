# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** two threads. (1) Finish the TV/phone arc: everything is
verified on the owner's phone; what remains is the actual parents'-TV
deployment with `http://jellyfin.cp1.mesh.internal:30096`. (2) Mesh
policy as data (sketch `6462fed4`): phases 1–2 are live — git file +
ephemeral wallet-gated overlay on `/policy` (ADR-0014, Proposed).
Next is phase 3: `/policy` endpoint + device hot-reload, then phase 4
(apid push to nodes + unseal reconciliation).

**Toward goal:** both serve "Mesh v2" in `desired-state/goals.md` —
the TV client closes its last open slice, and policy-as-data extends
"one derivation tree" to the access rules themselves (invariant 2:
git-derived, never server-owned; the overlay is deliberately unable to
outlive the process).

**Out of scope:**
- Phase 4 before phase 3's hot-reload loop proves itself; a node-side
  policy agent is rejected outright (ADR-0014).
- Widening the media group's firewall (:80 ingress) — re-enroll the
  device as `admins` instead if it matters.
- Any storage migration while `longhorn-bulk` is faulted (w1 down).
