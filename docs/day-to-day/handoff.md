# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-19 (second session) — **the identity plane carries real
traffic.** `irohup` (`359.8.4`) enrolled the laptop with one wallet
signature, beat the hub, and at **14:01:47Z** `talosctl version` ran
through cp1's `apid` facet — the first real caller on the plane —
followed by `kubectl get nodes` through `kube-api` (164 KB, 158 ms).
Both dial **by name**, with the bundle on connect. Commits `a686396`
`f0ce86e` `0cd3f2a` `464e286`.

- **`config-server/cmd/irohup`**: v2 enrollment (`enrollmsg` v2 → hub
  answers `{config, kit}`; nebula key and artifact stay nebup's, so one
  signature enrolls both planes), state dir `~/.config/talos-mesh/
  <name>.iroh/` with the node's layout, and TCP bridges
  `<member>/<facet>=<listen>`. `walletsign` learned the `node` field;
  `devkey.LoadOrCreate` is shared with nebup.
- **`nodeagent` is now the member runtime**: empty `Forward` ⇒
  caller-only (no ALPN, no consent), `caller.go` adds
  `Present`/`Resolve`/`Dial`. `iroh-go/cmd/p0agent` deleted — both its
  halves are superseded.
- **Two bugs found by running it**, both fixed and pinned:
  (a) **`seq` exactness** — JCS numbers are IEEE-754 doubles, so a
  `UnixNano` seed collapsed `#renew` + `#bundle` in one beat onto one
  seq, *and* parked the hub's high-water mark beyond every honest seq
  (40 min lockout, cleared only by redeploy). `envelope.MaxSeq`
  (2^53−1) is now refused on both sides; agents seed `UnixMicro`
  (protocol ADR-0006, Proposed).
  (b) **name-map reconvergence** — the hub's witness cache is empty
  after a deploy, so the first member to beat lost its peers;
  `nodeagent.mergeNameMaps` keeps its own unexpired, unblocked entries.
- **`tqr` done**: `/sealed` 503s on identity sealed/nag when the hub
  serves an identity plane (`--iroh-relay`); a dev run without it only
  reports. **`kql` done**: `marnyg-iroh-relay-spike` destroyed,
  `fly/relay-spike/` removed.
- **Exit checks (`359.8.6`) all three pass.** Reboot: cp1 back and
  re-admitted in 52 s, unaided. Hub re-seal: two deploys + unseals; cp1
  and irohup both renewed at the rotated hubkey and resumed — cp1 did
  renew + `#bundle` in one beat three times, the exact case that broke.
  Check 3 from `mar@nixos` (this laptop is relay-only): a second member
  enrolled there, LAN-direct `Ip(10.0.0.11->10.0.0.64)` at 8–10 ms;
  loopback-bound it falls to the relay at 47–51 ms and the bridge keeps
  working; restored, it is direct again.
- cp1 upgraded **through the bridge** to `p0agent` 0.1.2 (the plane
  carried its own upgrade); `minipc.yaml` pins the new digest.

## Loose threads

- **Check 3 verified path modes, not live migration**: its two legs are
  separate process starts, so mid-connection roaming (address changes
  under a live QUIC connection) is still untested — that needs a
  physically roaming device, i.e. Phase 2 mobile.
- **The `mar@nixos` member is enrolled but stopped** (owner ruling
  2026-09-19). Its state is intact at `~/.config/talos-mesh/nixos.iroh/`
  (NodeId `ed:c02908be…`), so restarting `irohup` there needs no wallet
  act; its certs renew on the next beat or expire in 90 d.
- **Enrolling a headless member needs an ssh tunnel today** (the
  signing page binds loopback on the enrolling host). `irohup` has no
  device-flow mode, though the hub already accepts `node=` on
  `/mesh/enroll/device` — filed as `talos-config-4ps`.
- **A pre-merge binary can still narrow a peer's map**: the merge only
  protects the process that runs it, and the *persisted* map is
  whatever the last writer saved. Seen live — an old irohup process
  beat at 16:55 and saved a 1-name map that the new one then had
  nothing to merge from. Recovery is one forced beat per side.
- Protocol **ADR-0006 Accepted** 2026-09-19; root ADR-0024 still Proposed.
- `irohup` is running in the foreground on the laptop (bridges on
  `127.0.0.1:50000` / `:6443`); `talosctl` needs
  `127.0.0.1 talos-wu6-eib` in `/etc/hosts` (added this session).
- cp1's DHCP address moved twice more (`.62 → .64`). The plane never
  noticed; only direct LAN access does.
- Carried: w1 (`0q0`, `kso`), `5gz`, `DefaultMailbox`/beat fraction.

## Suggested next steps

- **Phase 2 starts at `359.9.1`**: point the admin CLI paths at the
  `irohup` bridges in anger — talosconfig/kubeconfig endpoints, and
  whatever still assumes nebula (`nix run .#apply` dials the overlay
  address). Phase 1 closed 2026-09-19; `359.8.2` (Provisioner-as-actor)
  moved to Phase 2.

- Then Phase 2 (`359.9`): admin CLI paths onto the bridges in anger
  (`359.9.1`), which is mostly "stop using nebula for talosctl".
