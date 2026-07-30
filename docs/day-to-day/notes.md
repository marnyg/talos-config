# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review). -->

- 2026-07-29 — ~~`nix run .#apply` is dangerous on provisioned machines~~
  Fixed later same day (#30): apply now fetches the hub-composed config
  over the tunnel and refuses to compose locally.
- 2026-07-29 — Every fly deploy re-seals the hub: derived roles (wg,
  KMS, enrollment) are down until a wallet unseal at `/status`. The
  tunnel HTTP listener (hello + admin `/config`) only exists post-unseal.
- 2026-07-29 — ~~`nix run .#apply` now requires being on the wg tunnel as
  an admin peer (`wgup`) and an unsealed hub. `APPLY_HUB` overrides the
  default `http://10.99.0.1`.~~ Superseded by step 3 (2026-07-30):
  apply rides the mesh as an admin device (`nebup`), default
  `http://10.42.0.1`.
- 2026-07-29 — ~~`wgping` binaries built before the tunnel-HTTP change
  hang against the new hub.~~ Tool deleted with wg0 (2026-07-30);
  offline derivations moved to `cmd/recover`.
- 2026-07-29 — nebula cert-version skew: `nebula-cert` ≥1.10 emits V2
  certs by default; nebula ≤1.9 (e.g. alpine's apk package) can't parse
  them. Anything minting or consuming mesh certs must be ≥1.10; the hub
  embeds 1.11.0. (Now also recorded in `technical/guides/gotchas.md`.)
- 2026-07-29 — mesh names resolve but nothing answers on them yet:
  `<name>.mesh.internal` is served by the hub from derived state, while
  machine and device certs are not minted until the node side lands.
  Node-side injection is written as of later the same day, so this holds
  only until the deploy + `talosctl upgrade` + `apply` sequence runs.
- 2026-07-29 — the node's mesh identity arrives with its *config*, and
  the nebula extension arrives with its *installer image*. Both are
  needed: a node on the new schematic without a re-fetched config runs
  nebula with no config file, and a re-fetched config on the old
  schematic carries an ExtensionServiceConfig no service consumes.
  Neither state is harmful, but neither is on the mesh.
- 2026-07-29 — a node's overlay firewall lives in its *stored config*, so
  changing `nodeNebulaConfig` (adding the media rule, say) does nothing
  until every node re-fetches and re-applies. ~~cp1's applied config
  predates the media group~~ Re-applied later same day (`84d7ca5`);
  the media rule is live on cp1.
- 2026-07-29 — the laptop's wireguard interface is named `talos-laptop`,
  **not** `wg0` — `ip link show wg0` proving absence proves nothing. It
  is kernel wireguard with no owning daemon (`wg show` output can still
  be empty — check for `kworker/R-wg-crypt-talos-laptop`); take it down
  with `sudo ip link del talos-laptop`, restore with `wgup`.
- 2026-07-29 — **any mesh measurement taken with the wg tunnel up is
  poisoned**: the lighthouse advertises cp1's wg and pod IPs
  (`10.99.0.54`, `10.244.x`) and a dual-overlay client will "punch"
  straight through wireguard — nebula-over-wg-over-fly, double
  encryption, hairpinned. Symptom: handshake `from="10.99.0.54:4242"`.
  Two criterion-2 runs were invalidated by this before it was caught.
- 2026-07-29 — first mesh tunnel takes ~6s to converge: the initial
  pings are dropped during lighthouse query + punch, then arrive relayed
  (~27ms via fly), then direct (~1.8ms on the LAN). Steady-state latency
  is the number that matters; the loss is setup, not packet loss.
- 2026-07-29 — the upgrade→apply gap is safe to sit in: cp1 spent ~5
  minutes with `ext-nebula` in `[Waiting]: Waiting for extension service
  config` and nothing else degraded (apid up, etcd fine, media stack
  untouched). Deploy the extension and the identity in either order; the
  node just is not on the mesh until both land.
- 2026-07-29 — a mesh derivation error (address collision, bad `meshIP`)
  now refuses the whole `/config` serve, provisioning included. Pure
  derivation, so no overlay dependency, but it does mean a repo mistake
  in mesh addressing blocks installs — the same posture wg0 already has.
- 2026-07-29 — ~~the mesh is enabled per-deploy with `--mesh-port` and
  requires `--wg-port`… a mesh startup failure is non-fatal, `/sealed`
  still returns 200.~~ Inverted by step 3 (2026-07-30): `--mesh-port`
  is standalone and `/sealed` returns **503 on mesh startup failure**
  (it still never blocks the unseal itself — KMS rides the WAN,
  invariant 4).

- 2026-07-30 — **Any overlay carrying the route to the hub/peer poisons a
  punch measurement**: nebula will use it as underlay and hairpin. Bit us
  twice with wg0; the office run survived only because Tailscale happened
  to have no exit node set. Portable pre-flight before any punch test —
  `netstat -rn` plus `route get <peer-ip>` (macOS) or `ip route get`
  (Linux) — confirm egress is a physical NIC. The old `ip link` check
  silently no-ops on macOS, which is where these tests actually run.
  Guard filed as a bug against `nebup` (task 42).
- 2026-07-30 — **The office MacBook is enrolled as device name `laptop`**,
  the same default the home laptop used. Identity derives from (master,
  name), so both machines hold the *same* mesh key and the same overlay
  address — do not run nebula on both simultaneously. It also means a
  corporate-managed machine holds an owner-device mesh credential;
  revocation route is `talos/mesh-blocklist.txt`. _Decided later same
  day (d8a8ed86): leave as-is; revisit only if it becomes a problem._
- 2026-07-30 — Home network has **no native IPv6** (no v6 default route;
  the only global-scope v6 address is a Tailscale ULA). This is the
  blocker on ADR-0006's revisit trigger for direct remote paths.
- 2026-07-30 — **cp1's LAN lease drifts freely**: 10.0.0.20→.30
  overnight, .30→.31 across a single reboot. `talosctl` contexts and
  muscle memory pointing at a LAN IP are stale within days; find the
  node with a port-50000 scan (`echo > /dev/tcp/10.0.0.N/50000`). Mesh
  and wg addresses were stable throughout. Phase 2 step 2 retires this.
- 2026-07-30 — **Mobile Nebula on the phone was configured by hand**:
  the `/mesh/tv` flow delivers a self-contained YAML, but the app has no
  config-file import — CA/cert/key and lighthouse/relay/static-host-map
  details were entered through its site UI. Workable but clunky; recurs
  at every 90-day device-cert renewal (`nebDeviceCertValidity`). A
  re-enroll mints the same identity, so renewal is re-entry, not
  re-approval of anything new.
- 2026-07-31 — **the cluster endpoint is now cp1's mesh address**
  (`10.42.218.125`): kube API reachability from a node rides nebula0
  existing locally (`ext-nebula` extension service), not a native Talos
  interface as with wg0. The tun comes up without lighthouse
  reachability, and the LAN-address SANs keep `talosctl`/`kubectl`
  recovery working at the DHCP address. An endpoint apply costs ~30s of
  apiserver connection-refused; it reconverges unaided.
- 2026-07-31 — anything cached that points at `10.99.0.54` (old
  kubeconfigs, talosctl contexts, muscle memory) still works while the
  dual overlay runs, and dies at step 3 when wg0 is stripped. The
  checked-out `talos/talosconfig` + `kubeconfig` are already re-pointed
  at `10.42.218.125`.
- 2026-07-30 — **the hub re-mints its own nebula leaf at every unseal**:
  the hub's leaf fingerprint rotates per deploy+unseal cycle while the
  issuer (derived CA `b881d6ff…`) stays fixed. Never pin the hub's leaf
  fingerprint anywhere — pin the CA.

- 2026-07-30 — **wg0 is fully deleted** (phase 2 step 3): anything
  referencing `10.99.0.x`, `wgup`, `wgping`, `talos.wg`, or udp/51820
  is dead — including the wg-flavoured notes above, kept struck-through
  for history. Laptop cleanup owed: remove any leftover `talos-laptop`
  wireguard interface / wgup autostart; `/tmp/wg-talos.conf` is
  obsolete (resolves the first absorbed-handover item below).
- 2026-07-30 — **first `apply` after a hub redeploy can time out**:
  the laptop→cp1 mesh tunnel needs lighthouse re-registration + a
  fresh handshake after the hub restarts. Warm it with
  `ping 10.42.218.125`, then retry — succeeded on second attempt.
- 2026-07-30 — the cached `~/.config/talos-mesh/laptop.yml` predates
  phase 2: it still carries the retired wg0 underlay-filter entry and a
  hand-set `tun.dev: nebula1` the hub never rendered. Harmless (nebup
  honors the existing dev name), but `nebup -reenroll` refreshes it —
  and split-DNS answers will then appear on `nebula0`, the name nebup
  pins itself.
- 2026-07-30 — hub-redeploy mesh warmup reconfirmed on the plain
  deploy+unseal path (not just `apply`): cp1 unreachable over the mesh
  for ~45–60s after unseal, then recovers unaided — first pings
  relayed/lost, then 1.2ms direct. Don't page on the first failed ping
  after a deploy.
- 2026-07-30 — `MESH_CA_PIN` in fly.toml pins the derived mesh CA
  (`b881d6ff…`); a wrong-wallet unseal now fails loudly instead of
  bringing up an untrusted mesh. Re-derive offline with
  `recover -ca-fingerprint -sig <unseal-sig>`.

- 2026-07-31 — **cp1 reprovisioned onto the new disk layout**: node name
  is now `talos-ezw-edv`, LAN lease `10.0.0.32`. Partitions: p3 STATE
  (luks), p4 EPHEMERAL 160GiB (luks), p5 `u-media` 300GiB (xfs, plain)
  at `/var/mnt/media`.
- 2026-07-31 — **a plain `talosctl reset` wipes the whole disk,
  media library included.** The standard reinstall is now
  `reset --system-labels-to-wipe STATE --system-labels-to-wipe
  EPHEMERAL`: it *deletes* those partitions (not just contents), the
  bootloader survives, the node returns in maintenance mode on a LAN
  lease, and `apply-config --insecure` + `bootstrap` rebuilds — no USB
  medium needed. u-media is untouched and re-adopted by label.
- 2026-07-31 — the media library is **empty** post-migration (old
  `/var/media` went with the wipe, as agreed). u-media re-adoption
  across a reinstall is proven by mechanism but not yet exercised.

### Absorbed from the legacy `handover.md` (2026-07-24), still open

These were unchecked loose ends when that file was written; none have
been verified since, so treat them as unconfirmed. Not filed as tasks —
ask before doing so.

- Laptop WG config lives in `/tmp/wg-talos.conf` — move somewhere
  permanent + `chmod 600` (user action).
- `argocd-dex-server` pod in Error — unused OIDC component; candidate
  for disabling in the ArgoCD install.
- `tailscale-operator` Application Degraded — never investigated. Worth
  a look given it may be moot once the mesh lands.
- KMS slot 0 is never used at boot (early-boot DNS loses the race).
  Accepted; a KMS endpoint by IP instead of hostname would dodge DNS.
- Old kubeconfigs pointing at `10.0.0.x` are dead — regenerate.
