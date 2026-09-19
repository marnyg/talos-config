# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 1 — identity plane beside nebula** (`359.8`,
decision `b2t`). Dual plane: nebula untouched, the iroh plane grows
next to it. The hub is a working set of protocol actors reachable by
key (hubkey = iroh `EndpointId`, `e8d`), `#bundle` is complete with a
witnessed name map (`2fc`), and **since 2026-09-19 cp1 is a real
member** (`359.8.3`): boot-token enrollment (ADR-0015, now Accepted),
a renewal beat every 6 h, `authorize()` on ALPN-gated stream facets
(`apid`, `kube-api`) rooted in the node's own consent. **Now: the
first real caller.** `359.8.4` (irohup) enrolls a device, beats, and
dials `cp1`'s `apid` facet with its bundle on connect — `talosctl`
through the identity plane is the test that closes the loop. Then
`kql` tears the scratch relay down, `tqr` flips `/sealed`, and the
exit checks (`359.8.6`) are event-based.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016)
and **Sovereign-actor protocol at the center** — the node is the
protocol's first real receiver outside the hub; its two runtime asks
(`SeqBase`, `Observe`) join `Hold`/`Multi` as consumer-driven protocol
additions.

**Out of scope:**
- Phase 2 consumer moves (talosctl/kubectl bridges in anger, gateway,
  Android app swap, k8s off the mesh) until Phase 1's exit checks pass.
- Relay access gating (`5gz`); Provisioner-as-actor (ADR-0024).
- Parents'-TV deployment (`4te`); storage work until w1 returns.
