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
(`apid`, `kube-api`) rooted in the node's own consent. **Since 2026-09-19 the
plane carries real traffic**: `irohup` (`359.8.4`) enrolls with one
wallet signature, beats, and bridges `talosctl`/`kubectl` onto cp1's
`apid`/`kube-api` facets, dialing by name with its bundle on connect.
`kql` (scratch relay) and `tqr` (`/sealed` 503s on identity) are done.
**All three exit checks
passed 2026-09-19** (`359.8.6`): reboot unaided in 52 s, hub re-seal
reconverges, and from `mar@nixos` the LAN path is direct (8–10 ms)
while a member with no LAN candidate rides the relay (47–51 ms) and
re-punches direct when one returns. **Phase 1 is done; Phase 2
(`359.9`) is next.**

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
