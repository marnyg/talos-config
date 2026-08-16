# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** two threads. (1) Finish the TV/phone arc: the app is now
debuggable and DNS-verified on the owner's phone; what remains is the
actual parents'-TV deployment with the proven
`jellyfin.cp1.mesh.internal:30096` recipe. (2) Mesh policy as data
(sketch `6462fed4`): phase 1 landed — `talos/mesh-policy.yaml` is the
who×what table; next is the ephemeral hub overlay + policy UI
(phase 2), then live sync (phases 3–4).

**Toward goal:** both serve "Mesh v2" in `desired-state/goals.md` —
the TV client closes its last open slice, and policy-as-data extends
"one derivation tree" to the access rules themselves (invariant 2:
git-derived, never server-owned).

**Out of scope:**
- Live policy push (phases 3–4) before the overlay+UI loop proves the
  experiment workflow; a node-side policy agent is rejected outright.
- Widening the media group's firewall (:80 ingress) — re-enroll the
  device as `admins` instead if it matters.
- Any storage migration while `longhorn-bulk` is faulted (w1 down).
