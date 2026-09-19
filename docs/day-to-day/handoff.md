# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-19 (fourth session) — **P2.1 closed and the hub is on the
identity plane** (`359.9.1`, `359.8.2.4`). Every admin path runs over
the irohup tun with nebula down on the Mac: talosconfig, kubeconfig,
`nix run .#apply`. Commits `56292d2` `77f72f1` `ba0a9af` `f03acf5`;
nixos bumped; hub redeployed (hubkey `8b723ff8…`, unsealed).

- **`-n` is the node's hostname.** apid on a control plane never
  short-circuits to itself (`director.go`: with a client cert every
  `-n X` is dialed as `X:50000`, SNI `X`), so `nodes:` must be a name
  cp1 resolves for itself *and* carries in its apid SANs — its
  hostname, via its own `/etc/hosts`. cp1's is the generated
  `talos-wu6-eib` (its patch never pinned `hostname:`; w1's does);
  the pin waits for the next reinstall (`t7b2`, blocked by `bsj` —
  Longhorn replicas are bound to the node name). `talosconfig`:
  `endpoints: [cp1.mesh.internal]`, `nodes: [talos-wu6-eib]`.
- **hub-http facet** (`config-server/hubfacet.go`, untagged +
  `hubiroh.go` adapter): the hub's wan endpoint binds the hub's stream
  ALPNs; each connection runs `cert.Authorize` with hubkey as receiver
  — a consent to the wallet for the hub's stream facets (target
  hubkey, cached per speak-as), then the recipe's grant and the member
  cert. Admitted streams are HTTP connections to `hubFacetMux`; the
  facet admits by recipe (admins *and* media), `/config` gates
  `admins` per route. C-free test `hubfacet_test.go`; e2e in
  `TestNodeAgentEndToEnd`.
- **`hub.mesh.internal`** resolves on the tun from the daemon's hub
  record (`nodeagent.HubName`): the hub is a well-known actor, not a
  member, so it is not in the name map. `:80` reads in the hub's
  vocabulary (`FacetPort("hub-http") = 80`).
- **Nebula `/config` route removed** (decision `d3z3`: a migrated
  consumer cuts its nebula path; `/hosts`, `/policy` stay for the TV).
- **`resolveMemberNames: true`** on both nodes → `-e cp1.mesh.internal
  -n w1` fans out. `apply` = hub over hub-http + `-e <cp>.mesh.internal
  -n <hostname>` (per-node names failed: w1 has no agent). `kso` done
  on the way (w1 is on; `no_turbo=1`, NTP verified).
- New `nix run .#kubeconfig` (server → `https://cp1.mesh.internal:6443`).

## Loose threads

- **w1 is not on the identity plane** (`qb5q`): factory image, no
  `p0agent`; reached only through cp1's apid proxy. Also carries
  `0q0` (replicas: 2) — w1 counts as "a node landed" now?
- **Fly hub image lags HEAD by one cosmetic change** (`GET /{$}` on
  the overlay hello, `f03acf5`); redeploy with the next real change.
- **Route-churn restart path still unobserved** (`7c3`).
- **Control socket not built** (`fgr`). ADR-0024/0025 Accepted this
  session. Mobile's own `netstack.go`/`dns.go` (`phz`).
- **The hub reads its git blocklist at authorize time** for hub-http
  (mirrors `Issuer.blocked` for `#renew`/`#bundle`). Invariant 2's
  "verifier never reads git" is met by nodes (bundle copy); the hub is
  the compiler — flagged for the next model review, not changed.
- The apid comment in `nebmachine.go` and the meta.yaml `ip` comment
  were rewritten; `ip` stays only because nebula config composes from
  it.

## Suggested next steps

- **P2.2 (`359.9.2`)**: hub→node dials (`/status`, bootstrap probes)
  onto identity streams; cut the `nebstack` dial path as it lands
  (`d3z3`).
- **`qb5q`**: point `alienware-x15.yaml` at the imager-built installer
  and upgrade w1 — then `w1.mesh.internal` exists and `359.9.2`'s
  probes cover both nodes.
- Fire `7c3` once deliberately.
