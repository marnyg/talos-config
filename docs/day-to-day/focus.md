# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** **Mesh v3 Phase 2 — consumers migrate one at a time**
(`359.9`), each step reversible, nebula still installed. First:
`359.9.1`, the admin CLI paths — `talosctl`/`kubectl` stop reaching cp1
over nebula and use the `irohup` bridges in anger. That means the
talosconfig/kubeconfig endpoints, the `/etc/hosts` SAN workaround, and
everything that still assumes the overlay (`nix run .#apply` dials the
nebula address today).

**Phase 1 closed 2026-09-19** (`359.8`): the identity plane exists
beside nebula and carries real traffic — hub actors on the hubkey, cp1
a member, `irohup` a caller, policy compiled to grants, all three exit
checks passed. Its one remainder, Provisioner-as-actor (`359.8.2`,
ADR-0024), moved into Phase 2.

**Toward goal:** **Mesh v3** in `desired-state/goals.md` (ADR-0016):
members dialed by key, IP as device-local fiction. Phase 2 is where
that stops being a second plane and starts being *the* path for a
consumer.

**Out of scope:**
- Phase 3/4 (nebula removal) until every Phase 2 consumer has moved.
- Fake-IP presentation (SOCKS/PAC or TUN) — Phase 2.4, after the CLI
  paths; `irohup` ships TCP bridges only.
- Relay access gating (`5gz`); Parents'-TV deployment (`4te`); storage
  work until w1 returns.
