# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** ADR-0012 first slice landed on 2026-08-14. Wallet-signed
enrollment (v1 message), device-generated keys, /status approval form,
cert-and-derivation `/config` gate, `nebup` reworked to the two-file
cache with `-group` and `-rekey`, `MESH_DEVICES`/`MESH_MEDIA_DEVICES`
gone from `fly.toml`. All package tests green. Not yet browser- or
device-verified against the real hub; deploy + smoke test is the next
concrete step.

**Toward goal:** "Mesh v2 — phones/TV join the network, one overlay,
one derivation tree" in `desired-state/goals.md`, and the same
"one human act" property the provisioning goal names: a device joins
with one wallet signature, no commit, no deploy, no key in transit.

**Out of scope:**
- Storage work (emptyDir→Longhorn migration) — blocked on w1's
  physical revival; do not start while `longhorn-bulk` is faulted.
- The TV/phone APK (`2e1bef85`) — deferred until a remote-TV need;
  phone path accepted-broken meanwhile.
- 90-day renewal automation (`49443c38`) — carried; the device-key
  persistence makes it doable but not the first move.
