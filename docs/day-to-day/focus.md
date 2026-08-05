# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Implementing ADR-0012 — wallet-signed device enrollment.
Design is pinned (Branches 1–4 walked 2026-08-05); the hub-side
verify+mint core and the reworked nebup are the first movers, the
`/status` approval form rides with them. The declared device list
(`MESH_DEVICES`/`MESH_MEDIA_DEVICES`) and hub-derived device keys are
what this deletes.

**Toward goal:** "Mesh v2 — phones/TV join the network, one overlay,
one derivation tree" in `desired-state/goals.md`, and the same
"one human act" property the provisioning goal names: a device joins
with one wallet signature, no commit, no deploy, no key in transit.

**Out of scope:**
- Storage work (emptyDir→Longhorn migration) — blocked on w1's
  physical revival; do not start while `longhorn-bulk` is faulted.
- The TV/phone APK (`2e1bef85`) — deferred until a remote-TV need;
  phone path accepted-broken meanwhile.
- Signed-message prefix versioning and renewal automation — carried as
  open design items, not part of the first implementation slice.
