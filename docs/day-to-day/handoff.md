# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-19 (third session) — **P2.0 desktop presentation is live on
the Mac** (`359.9.6` closed). `talosctl -e cp1.mesh.internal -n
10.42.218.125 version` → cp1 `v1.12.6`, resolved by mDNSResponder via
`/etc/resolver/mesh.internal`, into a utun, through gvisor, one iroh
stream per flow, with the daemon running as `_talosmesh`. Commits
`2f3e3cf` `5b8c4c4` `af727e7` `3077c1b`; nixos `f46d4ad`.

- **Decision `8j3` superseded by `fgr`.** The session opened by
  re-evaluating "single root daemon, not privsep". Its load-bearing
  claim — Go's `Setuid` is thread-local on Darwin — is backwards:
  Linux is the per-thread-credential outlier (`AllThreadsSyscall`);
  XNU keeps creds on the proc. Tested (`/tmp/setuid-darwin`, 8 pinned
  threads all lost root). Its premises 2/3 also contradicted each
  other and misread invariant 2 (the durable key is the credential,
  not "state in hostile storage"). Result: **root-launched,
  privilege-dropped single daemon** — dominates both options `8j3`
  weighed.
- **`config-server/fakeip`**: the presentation layer extracted from
  `iroh-go/mobile` with the link injected — `TunLink` (wireguard-go
  `tun.Device` ↔ gvisor `channel`, portable replacement for linux-only
  `fdbased`), a **map-gated** split-DNS `Resolver` (spike `eda`'s fix:
  answers only names the agent's name map knows, forwards or NXDOMAINs
  the rest, so nebula keeps `jackett.cp1` during coexistence), darwin
  `Setup`/`RouteIntact`. 7 tests incl. a packet round-trip through a
  fake device, race-clean.
- **`irohup -tun`**: `privilegedSetup` (utun, `198.18.0.1`, route
  `/15`, chown state dir, setgroups/setgid/setuid, asserts euid ≠ 0
  and that root cannot be regained) → `serveTun`. Bridges and tun
  share `connPool`. `-state` is self-contained (nebula files beside
  it). `-enroll-only` is the daemon's enrollment handoff.
  `policy.FacetPort` is the one table for a facet's natural port.
- **nixos**: `modules/darwin/services/talos-mesh.nix` — launchd daemon,
  `_talosmesh` (uid 560), `KeepAlive.PathState` on `kit.json` (no
  crash-loop before enrollment, self-start after),
  `/etc/resolver/mesh.internal → 198.18.0.2`, `talos-mesh-enroll`
  (runs as the service user, opens the wallet URL as you). Flake input
  `talos-config` with its own nixpkgs.
- Spike `eda` closed: zone `mesh.internal` inherited, verified live.

## Loose threads

- **`-n` for talosctl is resolved on cp1's side.** `-n
  cp1.mesh.internal` → cp1's apid asks `127.0.0.53` and fails; `-n
  cp1` → zero addresses; the nebula IP works but dies at Phase 4. P2.1
  (`359.9.1`, noted) must pick a node identifier cp1 knows itself by.
- **Route-churn restart path unobserved** (`7c3`): the `/15` survived
  a Wi-Fi toggle with ethernet primary (utun-scoped route, configd left
  it alone). Primary loss and sleep/wake untested; the exit-1 →
  KeepAlive-restart rule has never fired live.
- **The Mac now has two irohup identities' worth of state**: the
  daemon's `/var/lib/talos-mesh/marius-mac.iroh` (enrolled today,
  NodeId `ed:14a8ca…`) and nothing under `~/.config/talos-mesh/` — so
  the old foreground bridge mode is gone here. `mar@nixos` unchanged.
- **Control socket not built.** `fgr` carries the constraint
  (LOCAL_PEERCRED, written allow-list); the daemon has no local
  surface yet — status is `/var/log/talos-mesh.log`, re-enroll is
  `talos-mesh-enroll -reenroll`.
- **Mobile still carries its own `netstack.go`/`dns.go`** with the
  ungated `lookup()` (`phz`, P3): adopting `fakeip` drags
  config-server's module graph into the gomobile build.
- **`dig` does not honour `/etc/resolver`** (reads `resolv.conf`
  directly). Probe with `dscacheutil -q host -a name …` or a real
  client; `dig @198.18.0.2` for the resolver itself.
- Root ADR-0024 still Proposed. No ADR yet for the desktop
  presentation architecture (`4fm` + `fgr`) — offered this session.
- Carried: w1 (`0q0`, `kso`), `5gz`, `4ps`, `DefaultMailbox`/beat
  fraction.

## Suggested next steps

- **P2.1 (`359.9.1`) on the tun, not the bridges**: talosconfig
  `endpoints: [cp1.mesh.internal]` + a `nodes:` entry cp1 resolves for
  itself; kubeconfig `server: https://cp1.mesh.internal:6443`; `nix
  run .#apply` off the nebula address (needs `359.8.2.4`, hub-http).
- Fire the churn path once deliberately (`7c3`): unplug ethernet with
  Wi-Fi off, watch `/var/log/talos-mesh.log` for a second `tun up`.
