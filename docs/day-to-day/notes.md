# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review).

     Pruned 2026-09-03: wg0-era and nebula-phase-1/2 history removed
     (struck-through entries live in git history before this date).
     Pruned 2026-09-16: Phase-0-gate-era cautions, stock Mobile Nebula,
     zitadel-era kubeconfig reason, duplicated gotchas.
     Reference beads issues by id (`talos-config-xxx`). -->

## Read first

- 2026-09-03 — **Read `desired-state/domain-model.md` §"The three
  layers" before any authority/identity discussion.** A design session
  lost an hour to "sovereign" applied to members and an invented
  "presence" concept; both are defined/retired there. ADR-0017 is
  *Proposed*: the running system is still nebula's receiver-side
  firewall, and `mesh-policy.yaml`'s nebula render is what executes
  until Mesh v3 Phase 1.
- 2026-09-03 — **Mesh v3 is in the tracker and on cp1's extension, not
  in the running mesh.** Nebula is the mesh until Phase 4. Deferred
  nebula-era issues (`cjo en6 4ns 41b 6gq ap2 90a`) were parked on the
  Phase 0 gate; the gate passed 2026-09-16 — re-triage them under
  `359.8`/`359.9` rather than closing.
- 2026-09-05 — **The Quint models are the sharper spec for
  ADR-0015/0017.** Five doc sentences were refuted and ruled the same
  day (decisions `h3c zqw dvf syw 6o1`; FINDING blocks in
  `verification/quint/{authorize,runway,approval}.qnt` record the
  trace). When the glossary and a model disagree, check the model's
  header first — it says which ruling applied.
- 2026-09-06 — **ADR-0018 is Proposed, not built.** The running unseal
  is still the KDF (`masterderive.MasterMessage` → master → CA); the
  `speak-as` unseal, per-process hub key and hub-as-actors split are
  design only. The domain model §2 and glossary describe the *desired*
  shape; `hubseal.go`/`masterderive` describe what runs. **Owner
  ruling 2026-09-06: nothing depends on the running system — break
  nebula-era code wherever the new shape needs it.** (Supersedes the
  2026-09-03 "don't fix nebula code toward ADR-0017" caution above.)
- 2026-09-13 — **Protocol ADR-0001 is Accepted and built** (M2 swarm,
  `0bc.2.1–.6`): `cert.VerifyChain` folds `Attenuate` over N links,
  `Authorize` is its `[g]` special case + group rule, `envelope/` and
  `actor/` exist, `iroh-transport/` is the iroh adapter module. The
  Quint model still leads: change `authorize.qnt` before the Go — the
  chain laws (`authorize_chain_laws_test.go`, 18) pin them 1:1.
  **Canonical cert form changed** (`postage` always emitted): no signed
  certs existed in-repo, but any cert signed before `40c1755` will not
  verify.
- 2026-09-16 — **ADR-0024 (hub actors cut by key) is Proposed and
  partly built** (2026-09-17: Issuer listens in-process, Enroll →
  `#mint-device`, relay child, `/.well-known`; 2026-09-18: `#bundle`
  and the iroh endpoint `e8d`; 2026-09-19: the name map — only
  Provisioner-as-actor is not). Decision `itb` (hub
  HTTP over a stream facet) is revised by `mdv`: `/hosts` and `/policy`
  will not exist over the mesh — don't build them; the beat is
  `#renew` + `#bundle`.
- 2026-09-18 — **Two recipe files, one live.** `talos/mesh-policy.yaml`
  (v2) is **frozen** except for emergencies and is what nebula
  enforces. `talos/mesh-policy-v3.yaml` is the compiler's input
  (`config-server/policy`, protocol ADR-0004 built) and `Issuer#bundle`
  signs its output (same day), but **no caller receives its grants
  yet** — ~~the Issuer's transport is in-memory until `e8d`~~ (landed
  the same day: the hub answers over iroh) but no member client exists
  (`359.8.3`/`359.8.4`), so a v3 edit changes nothing at runtime until
  they land. Same for
  `talos/mesh-blocklist-v3.txt` (ed: ids; v2's `mesh-blocklist.txt`
  stays the one nebula enforces). Its
  closed sets live in three places kept in step by tests
  (`mesh-policy-v3.ncl` ← `TestVocabularyMatchesNickel`, `policy.Groups`
  ← `mesh.Groups()`); change the glossary first, then all three.
- 2026-09-18 — **Touching `protocol/*.go` stales TWO `vendorHash`es**
  (`config-server/nix`, `iroh-transport/nix`) and a cached FOD hides
  it — `nix build` then fails with an "undefined: actor.X" that looks
  like a code bug (bitten again 2026-09-19). Recompute both:
  `nix build .#config-server-bin.goModules --rebuild` and
  `nix build .#iroh-transport.goModules` (plain — `--rebuild` errors
  when the old output was never built locally); CI job `vendor-hash`
  catches it on push.

## Mesh v3 spike infra (scratch)

- 2026-09-13 — **A second public surface exists on purpose:**
  `marnyg-iroh-relay-spike.fly.dev` (fly app of the same name, region
  `arn`, `fly/relay-spike/fly.toml`) runs `n0computer/iroh-relay:v1.1.0`
  as an **open relay** (`access = "everyone"`) for the Phase 0 probes.
  Owner accepted this as spike scope against invariant 5 (2026-09-13);
  gate ruling 2026-09-16: **stays up until Phase 1.2** embeds the relay
  in the hub — done 2026-09-17 (`359.8.2.2`), but cp1's `ext-p0agent`
  still dials the scratch app on every boot until `359.8.3` repoints
  it (`kql`). Deploy with
  `fly deploy -c fly/relay-spike/fly.toml`; `curl …/ping` → 200 is the
  liveness check (`/generate_204` is 404 in plain-HTTP mode).
  Registry tag `registry.fly.io/marnyg-iroh-relay-spike:p0peer` is a
  throwaway alpine + static `p0relay` for far-NAT peers
  (`fly machine run … --rm -- dial …`; `--file-local` hangs on 18 MB).
- 2026-09-13 — **The owner laptop is a relay-only peer.** Cisco Secure
  Client's socket-filter extension (+ Defender netext) returns `EPIPE`
  from `sendmsg` for unsigned binaries to any `en0` destination, so no
  LAN-direct measurement is valid from here; use two Linux hosts. The
  home LAN test bed is `mar@nixos` (x86_64 NixOS, 10.0.0.11, nix + docker,
  no sudo for the agent; firewall blocks inbound UDP on `wlp12s0` unless
  opened — the rule added 2026-09-13 lasts until reboot) plus Docker
  Desktop on the mac running the musl `p0relay` under `--platform
  linux/amd64`. In-process tests (`smoke`) punch to loopback even on
  the filtered laptop and prove nothing about the host path.
- 2026-09-13 — `iroh-ffi` tracing (`P0_LOG=debug` in `p0relay`, or
  `iroh.SetLogLevel`) writes to **stdout**, the Go `logf` to stderr —
  capture with `>file 2>&1`. Every startup probes UDP 7842 (QAD) on the
  relay for 3 s and times out (relay has no QUIC); harmless, silenced
  by `p5g`.

- 2026-09-15 — **cp1 boots an imager-built installer, not the factory
  schematic**: `ghcr.io/marnyg/talos-installer:v1.12.6-p0agent-0.0.3`
  = stock v1.12.6 + iscsi-tools + nebula + util-linux-tools +
  `p0agent` 0.0.3 (`ext-p0agent`, NodeId `7dd90eb3…`, key at
  `/var/lib/p0agent/key` on EPHEMERAL). Since the gate ruling
  2026-09-16 (`5cz`) this is **the declared image** in
  `talos/hardware/minipc.yaml`, pinned by digest; a `talosctl upgrade`
  back to `6a9acc…` would drop the agent. Rebuild:
  `talos/extensions/p0agent/build.sh <static-binary> <ver>` (needs
  docker + ghcr login; both ghcr packages are public and must stay so —
  the node pulls unauthenticated), then update tag + digest in
  minipc.yaml.
- 2026-09-15 — **Any Talos extension that mounts under `/var` needs
  `depends: - service: cri`**, or `talosctl upgrade`/`reboot` hangs at
  `teardownLifecycle` ("luks2-EPHEMERAL … still in use"). Symptom:
  `talosctl services` shows `ext-nebula`/`ext-iscsid` Finished and the
  offender still Running; `talosctl service ext-<x> stop` unblocks it.
- 2026-09-15 — **Upgrades on cp1 take ~10 min of drain** while w1 is
  down (evictions time out one by one); install + reboot is < 1 min.
  Use `--wait --debug` into a file, not a foreground tool call.
- 2026-09-15 — `talosctl` over the iroh bridge: `p0agent bridge -relay
  … -id 7dd90eb3… -listen 127.0.0.1:50000`, then `-e talos-wu6-eib -n
  talos-wu6-eib` with `127.0.0.1 talos-wu6-eib` in `/etc/hosts` (apid's
  SANs: node IPs, `cp1.mesh.internal`, hostname — not `127.0.0.1`).
  The laptop's wired `en7` gets LAN-direct paths; Wi-Fi `en0` is
  relay-only (Cisco filter, 2026-09-13 note).

- 2026-09-16 — **P0.2 scratch on the NixOS box was torn down at the
  gate** (same day): `p0agent-standin`, `~/p0-jf/`, the `/data/p0test`
  compose bind, the "P0 Test" library are gone; `jellyfin` was
  recreated from the restored compose. Still true about the box: the
  1TB disk `~/disks/1TB-old` is **100 % full** — never copy onto it;
  the login shell is **fish** (`ssh … bash -s <<EOF` for scripts); no
  passwordless sudo; UDP `41641` is Tailscale's; **LAN peers punch in
  through nixos-fw via conntrack with no inbound rule** (P0.2 step 6).
  `mar@nixos:~/p0` is kept as the x86_64 builder: a single-branch clone
  at `fa003f8` + scp'd files — not a checkout of the branch; sync by
  `scp`, rebuild with
  `NIXPKGS_ALLOW_UNFREE=1 nix-shell --impure iroh-go/android-p0/shell.nix
  --run 'IROH_FFI_ANDROID_LIB=/tmp/android-ffi-result/lib ./build-aar.sh
  && gradle --no-daemon assembleDebug'` (`/tmp/android-ffi-result` is a
  nix GC root only as long as nobody runs `nix-collect-garbage`).
- 2026-09-16 — **Phone testing = `adb`, from the Mac**: `nix shell
  nixpkgs#android-tools`; the Sony XQ-BQ52 only enumerates adb after
  USB debugging is on *and* the cable is re-plugged. The Files-app
  installer fails silently ("The app wasn't installed") on the debug
  APK after the Play Protect prompt; `adb install -r` works. Starting
  p0mesh **evicts Tailscale** on the phone (one VpnService). The
  Jellyfin *app* is required for Direct Play (Firefox has no MKV
  demuxer and buffers forever while pulling the raw stream). Server
  side: `curl /Sessions` with an API token shows `PlayMethod`.
- 2026-09-16 — **Outside the home LAN every iroh path is the fly relay**
  (QAD off ⇒ no WAN punch attempted): ~50–75 Mbps to a phone. Do not
  read a throughput number taken from outside as a design result;
  `p0agent serve`'s journal prints `paths=[*relay:…]` vs
  `*ip:10.0.0.x` every 5 s while bytes move. At home the same setup
  went `*ip` within 5 s and held 97 Mbps avg (2026-09-16).

## Hub / mesh (as running)

- 2026-09-17 — **The production hub relays iroh** at
  `https://marnyg-talos-config.fly.dev` (`/relay`, `/ping`,
  `/generate_204` proxied to an `iroh-relay` child; `/status` has an
  "iroh relay" row; child logs are prefixed `relay|`, level via
  `RELAY_LOG`). It is **open** (`access = everyone`) until `5gz`, and it
  runs while the hub is sealed. `RELAY_DISABLE=1` on the fly app turns
  it off. Probe: `p0relay listen/dial -relay https://marnyg-talos-config.fly.dev`
  (relay path only from the owner laptop — see the Cisco note).
- 2026-09-17 — **Deploy footguns found the hard way:** the Docker
  context is the *working tree*, not git — a gitignored
  `talos/extensions/*/_out` (145 MB) shipped into the image and
  overflowed `/dev/shm` at entrypoint `cp` (crash loop, "No space left
  on device"). `.dockerignore` now excludes `**/_out`; keep build
  outputs out of `talos/`. Locally, `docker run` needs
  `--shm-size=256m` for the same `cp`. And the image must carry
  `protocol/` beside `config-server/` (go.mod `replace`).
- 2026-09-17 — **v2 enrollment exists server-side only.** Sending
  `node=ed:<hex>` on `/mesh/enroll/challenge`, `/mesh/enroll` or
  `/mesh/enroll/device` switches the payload to JSON `{config, kit}`
  and requires the identity plane unsealed (503 otherwise). No client
  sends it yet; nebup/Android are v1 and unaffected.

- Every fly deploy **re-seals the hub**: derived roles (mesh CA, KMS,
  enrollment, DNS) are down until a wallet unseal at `/status`. The
  mesh HTTP listener exists only post-unseal. `/sealed` returns **503
  on mesh startup failure** but never blocks the unseal itself (KMS
  rides the WAN, invariant 4). (`talos-config-fbb`; gets heavier under
  Mesh v3 — relay identity derives from the master.)
- After a hub redeploy + unseal, cp1 is unreachable over the mesh for
  ~45–60 s (lighthouse re-registration + fresh handshake), then
  recovers unaided. Don't page on the first failed ping; warm with
  `ping 10.42.218.125` before `apply`.
- The hub **re-mints its own nebula leaf at every unseal** — never pin
  the hub's leaf fingerprint; pin the CA (`MESH_CA_PIN` in fly.toml,
  derived CA `b881d6ff…`). A wrong-wallet unseal fails loudly.
- A node's overlay firewall lives in its *stored config*: changing
  policy does nothing on nodes until `nix run .#apply`; devices pick it
  up on re-enrollment or `/policy` poll. A `/policy` overlay never
  changes the hub's *own* running firewall (hub scope renders at
  unseal) and every deploy drops the overlay — export first if it
  should survive.
- A mesh derivation error (address collision, bad `meshIP`) refuses
  the whole `/config` serve, provisioning included.
- **Any overlay carrying the route to the hub/peer poisons a punch
  measurement** (Tailscale exit node, another VPN) — nebula hairpins
  through it. Pre-flight: `route get <peer-ip>` (macOS) / `ip route
  get` must show a physical NIC.
- Home network has **no native IPv6** — the blocker on ADR-0006's
  revisit trigger (`talos-config-41b`, deferred under v3).
- The office MacBook and the home laptop are both enrolled as device
  name `laptop` — same address; do not run both simultaneously.
  Decided to leave as-is; revocation path is `talos/mesh-blocklist.txt`.
- **ADR-0012 is live**: enrollment mints only device-born keys.
  Pre-ADR master-derived device certs stay valid until their 90-day
  expiry. Re-enrolling under the same name keeps the same address.

## Cluster / Talos

- **cp1's LAN lease drifts freely** (moved four times in one day).
  Never hardcode it; the cluster endpoint is cp1's mesh address
  `10.42.218.125`. Find a node in maintenance mode with a port-50000
  scan. (Mesh v3 P2.5 replaces this with declared static LAN IPs.)
- Talos-generated node names are **not stable across reinstalls**
  (cp1 is currently `talos-wu6-eib`); w1 pins its hostname for this
  reason.
- Standard reinstall is `reset --system-labels-to-wipe STATE
  --system-labels-to-wipe EPHEMERAL` (bootloader survives, node returns
  in maintenance mode); a plain `talosctl reset` wipes the whole disk.
  See `technical/guides/reinstall.md`.
- `talosctl wipe disk <part>` does **not** free a user volume;
  `--drop-partition` is the real flag. Partitions renumber.
- Talos enforces the `baseline` Pod Security Standard on workload
  namespaces: hostNetwork/hostPort pods are silently forbidden (error
  only in `describe ds` events). Fix: `pod-security.kubernetes.io/
  enforce: privileged` namespace label via ArgoCD
  `managedNamespaceMetadata` (ingress-nginx has it).
- A machine waiting on device-flow approval has **no apid** (port
  50000 refused) — hardware is un-inspectable until approved. The hub
  logs mac + uuid + serial at `/device/code` time: `fly logs | grep
  "device auth started"` is how you write `meta.yaml`.
- `install.disk: /dev/sda` is a landmine on USB-booting boxes (w1);
  removing the key needs an RFC6902 patch (merge patches can't
  delete; `disk: ""` is silently dropped).
- A worker cannot reuse the cluster layer: workers take
  `worker-cluster.yaml` + `worker-secrets.yaml`; regenerate the latter
  with the `yq del(...)` one-liner in its header when `secrets.yaml`
  changes.
- `talosctl` flags are not global: `-n`/`-e` follow the subcommand;
  maintenance-mode reads are `get -i`, not `--insecure`.
- Use `--kubeconfig ./kubeconfig` from the repo root; `~/.kube/config`
  is not maintained.

## Workloads / storage

- **w1 is down** (since 2026-08-04; no LAN ping, no apid). <!-- stale? tracked by kso/0q0 --> Media
  library offline — all `longhorn-bulk` volumes `faulted` (single
  replica on w1). Needs physical attention; data presumed intact. Do
  not start storage work until it returns (`talos-config-kso`,
  `talos-config-0q0`). win2k25 system/ISO volumes are `degraded` (1 of
  2 replicas) and heal unaided when w1 returns.
- **Knowing deviation from invariant 2**: `longhorn-bulk` runs 1
  replica — the media library is neither git-derivable nor
  replicated. Accepted only until the new nodes land
  (`talos-config-0q0`). Wrong implementation, not a relaxed invariant.
- **Nothing app-level is replicated**: apps keep config on `emptyDir`;
  state dies on pod restart. "Longhorn is up" ≠ "state is safe".
- **Pods must not dial `.mesh.internal` names** — nebula routes
  10.42.0.0/16 sources, a pod's source is 10.244.x.x, so it only
  works while the pod lands on cp1 (70-min SSO outage, 2026-07-31).
  Rule: pods talk to Services; the mesh is for hosts and browsers.
  The issuer name is aliased to the siwe-oidc Service ClusterIP via
  `hostAliases` on every relying party (ADR-0010).
- ArgoCD polls git every ~3 min; nudge with `kubectl annotate
  application apps -n argocd argocd.argoproj.io/refresh=normal
  --overwrite`. Patching upstream-owned resources from git = SSA
  partial manifest with `argocd.argoproj.io/sync-options:
  ServerSideApply=true,Validate=false` (see
  `k8s/apps/argocd/server-patch.yaml`).
- ArgoCD-managed Jobs: never set `ttlSecondsAfterFinished` (prune-
  recreate loop); Jobs are immutable — bump the name on script change.
  `virtctl stop/start` is futile under selfHeal — VM state is a git
  edit of `runStrategy`.
- siwe-oidc: a bridge restart rotates the JWKS → ArgoCD sessions die,
  oauth2-proxy sessions survive. The image is `:latest` +
  `imagePullPolicy: Always` — CI push changes nothing until
  `kubectl -n sso rollout restart deployment siwe-oidc`.
- win2k25 reinstall is one API act: `scripts/win2k25-reinstall.sh`;
  RDP back ~25 min later. Password in secret `win2k25-admin`; rotating
  it requires resealing both that secret and the autounattend secret.
- jellyfin's admin password: `kubectl -n media get secret
  jellyfin-admin -o jsonpath='{.data.password}' | base64 -d` (the new
  pod is blocked on the faulted media volumes until w1 returns).

## Android app

- Distributed via the rolling `android-latest` GitHub release; every
  push touching `android/` or `config-server/{mobile,devkey}`
  re-clobbers `talos-mesh.apk`. CI is the only builder (no local SDK).
- The app pushes **no DNS server to the VpnService** (Android sends all
  device DNS to a VPN resolver; the hub only answers the mesh zone).
  Mesh names don't resolve on the TV; services are reached by IP from
  the app's host list. Superseded under Mesh v3 by client-side fake-IP
  resolution (`talos-config-359.9.4`).

## Tooling

- `nix build .#config-server-bin` runs the full go test suite in its
  checkPhase: any failing test on HEAD blocks devshell rebuilds and
  deploys. The devshell's quint is 0.30.0 (flake pin) vs 0.32.0 on
  current nixpkgs — models run on both; `verification/quint/
  _apalache-out/` is gitignored scratch.
- 2026-09-05 — `nix develop` needs `--impure` (devenv: "was not able
  to determine the current directory" otherwise). `check.sh verify`
  takes ~3 min, dominated by `approval.qnt` at depth 12.
- 2026-09-05 — **Quint laws over many nondet dimensions hold
  vacuously.** `authorize.qnt` with ~40 independent `oneOf`s reached
  Accept with p≈2⁻¹⁵; all 7 seeded mutants survived until a
  boundary-biased generator (valid scenario + ≤2 injected faults)
  was added. Always mutation-test a new model before trusting `[ok]`.
  Also: never `powerset()` a 25-element set in a generator.
- 2026-09-06 — **`iroh-go/` builds are slow cold and need the UA
  backport.** crates.io returns 403 to the generic User-Agent the
  flake's 2026-01 nixpkgs `fetchCargoVendor` sends; `iroh-go/nix/
  default.nix` overrides two lines of the vendor script (fixed-output,
  hashes unaffected) — delete it when nixpkgs is bumped past the
  upstream fix. Cold on an M-series: iroh-ffi 7–10 min, iroh-relay
  8 min, uniffi-bindgen-go ~45 min; warm `nix build .#iroh-go-smoke`
  44 s. Hand-run `go build` in `iroh-go/` needs
  `CGO_LDFLAGS="-L$(nix build .#iroh-ffi-static --print-out-paths)/lib"`.
- 2026-09-06 — `nix flake check --impure` is the canonical full check
  and is green since `81u` (18 YAML files yamlfmt'd, 0 semantic
  diffs). `--impure` is required by devenv, see the `flake.nix` header.
  yamlfmt quirk: a flow mapping that is the last sequence item before a
  comment gets a trailing `,}` — valid YAML, all parsers agree, ugly.
- 2026-09-06 — **rapid at the default 100 checks is too shallow for
  pair-faults.** In `protocol/cert` the m14 mutant (group rule as set
  overlap) died in only ~6 % of runs at 100 checks; the killing pair is
  ~0.06 % of samples. `TestFaultPairSweep` enumerates the model's fault
  space exhaustively (2424 scenarios, 0.7 s) — add a case there when a
  new law needs a specific pair. `check.sh verify` is now ~5.4 min
  (`approval` 162 s, `clock` 110 s).
- **`pi -p` hangs after completing its answer** when stdin is left
  open. Wrap non-interactive uses: `timeout -k 10 420 pi -p
  --no-session "…" </dev/null`.
- **Beads config lives outside git and can vanish.** `.beads/
  config.yaml` + `metadata.json` are gitignored; if missing, `bd`
  silently falls back to an empty DB named `beads` and reports "no
  open issues". Check `bd dolt show` says `Database: talos_config`.
  `export.auto` is `false`. Session-close check: `git ls-remote origin
  refs/dolt/data` must move after `bd dolt push`. Q-threads that are
  `blocks`-chained need `--force` to close with a reason.

## CI / orchestration

- 2026-09-12 — `.github/workflows/iroh-go.yml` is path-filtered
  (`iroh-go/**`, flake files). Measured on the 4-vCPU runner: `smoke`
  16 m 39 s cold / 45 s warm, `drift` 8 m 41 s (bindgen ~8 min there
  vs ~45 min on M-series). GH cache evicts after 7 idle days, so a
  quiet fortnight means a ~25 min cold run; add a weekly `schedule:`
  if that bites. **Upstream iroh's `.cargo/config.toml` pins
  `-fuse-ld=lld` for x86_64-linux** — the `iroh-relay` derivation
  `rm`s it in `postPatch`; keep that when bumping iroh.
- 2026-09-06 — Swarm orchestration via herdr: `herdr tab create` +
  `pane split` per worker, `herdr agent start <name> --kind pi --pane
  <id>`, `herdr agent prompt <name> "<pointer to brief>"`; poll with
  `herdr agent list | jq '.result.agents[]|select(.name!=null)'`.
  Workers write `~/git/swarm/_reports/<id>.md`; orchestrator rebases +
  `--ff-only` merges in dependency order and re-runs gates on `main`.
  _2026-09-12 update:_ `herdr worktree create --branch swarm/<name>`
  gives each worker its own workspace + worktree under
  `~/.herdr/worktrees/talos-config/`; briefs and reports live in
  `/tmp/swarm/<name>.{task,context,md}` (see `MANIFEST.txt`).
- 2026-09-13 — **`git pull --rebase` destroys merge commits.** The
  repo's default rebase flattened all six `merge swarm/*` commits of
  the M2 swarm (content intact, SHAs in bead notes gone). Either
  `git pull --rebase=merges`, or `git fetch` + inspect before merging.
- 2026-09-13 — **A worker cut off by API 429 is not lost.** The herdr
  agent stays alive and `idle` with its context; `herdr pane read
  <pane> --source recent-unwrapped` shows exactly where it stopped.
  Check that before re-doing work; uncommitted files survive in the
  worktree (`git status` there).
- 2026-09-13 — `nix flake check --impure` now includes
  `.#iroh-transport` (runs the iroh handshake suite, ~25 s warm).
  Hand-run `go test` in `iroh-transport/` needs both
  `CGO_LDFLAGS="-L$(nix build .#iroh-ffi-static --print-out-paths)/lib"`
  and `IROH_RELAY_BIN=$(nix build .#iroh-relay --print-out-paths)/bin/iroh-relay`
  (relay test skips without the latter). `.#iroh-transport-static`
  exists only on Linux; no Linux builder is configured on the Mac —
  the CI `static` job is the only place the musl link runs.
- 2026-09-13 — **`herdr worktree create` + `--base main` cuts from the
  local `main`**, so wave-2 workers see wave-1 merges only after the
  orchestrator merged them locally — merge before launching the next
  wave, never push-and-pull mid-swarm. Retire a worker right after its
  merge (`herdr worktree remove --workspace <id> --force; git worktree
  prune; git branch -d`); `create.json` holds the workspace id.
- 2026-09-13 — CI `static` job (iroh-transport.yml) is `continue-on-error`:
  a red `static` never fails the workflow. Check it explicitly with
  `gh run view <id>`; the job id is needed for `--log`.
- 2026-09-18 — **The next hub deploy asks for two signatures at
  `/status`** (decision `ce8`): the master message as before, plus
  the hubkey speak-as proposal. The proposal is per wallet and names
  the process's key — sign it only on that page (or `cast wallet sign`
  over the exact `<pre>` text); a copy is useless against any other
  process. Headless: `curl -d signature=… -d speakas_signature=…
  /unseal`; either alone works, the second must be the same wallet.
  Until `tqr`, `/sealed` only *reports* the identity plane — an
  unsigned speak-as does not page.
- 2026-09-18 — `config-server-bin` vendors `protocol/` via the go.mod
  `replace`, so its `vendorHash` (and `iroh-transport`'s) drifts on
  every `protocol/*.go` change and a cached FOD hides it locally.
  After touching `protocol/`, run
  `nix build .#config-server-bin.goModules --rebuild` and
  `nix build .#iroh-transport.goModules --rebuild`; the canonical
  caveat list is on `config-server-bin` in `flake.nix`
  (2026-09-18 later: moved to `config-server/nix/default.nix`, and the
  replaces now include `iroh-transport/` and `iroh-go/iroh` too).
- 2026-09-18 — **Hub deploys are `fly/deploy.sh`, not `fly deploy`**
  (`e8d`): the image is nix-built (cgo hub), `fly.toml` has no
  `[build]`, and a bare `fly deploy` fails on purpose. From the Mac:
  `HUB_BUILDER=mar@nixos fly/deploy.sh` — builds in that box's nix
  store (`--store ssh-ng://… --eval-store auto`; the darwin daemon runs
  as root and cannot use your ssh key, so `--builders` does NOT work)
  and pushes from there. The box's login shell is fish — anything you
  `ssh` over must be wrapped in `sh -c`. Musl Rust artifacts are warm
  there (`iroh-ffi-static`/`iroh-relay` for `x86_64-unknown-linux-musl`).
  Fallback without the box: `hub-image.yml` builds on every push;
  `workflow_dispatch` with `push=true` needs a `FLY_API_TOKEN` repo
  secret (not set yet).
- 2026-09-18 — **`go test ./...` in `config-server/` is still C-free**:
  the iroh binding is behind build tag `iroh` (`hubiroh.go`, stub in
  `hubiroh_stub.go`). To run the tagged suite by hand: `CGO_ENABLED=1
  CGO_LDFLAGS=-L$(nix build .#iroh-ffi-static --print-out-paths)/lib
  IROH_RELAY_BIN=$(nix build .#iroh-relay --print-out-paths)/bin/iroh-relay
  go test -tags iroh .` — `nix build .#config-server-bin` does exactly
  that. `--iroh-relay` on an untagged binary refuses at startup.
- 2026-09-19 — **cp1's LAN address changes on every reboot** (DHCP:
  `.58 → .59 → .62` in one afternoon). Direct access, no mesh needed:
  `talosctl -n <ip> -e <ip> --talosconfig talos/talosconfig …`. The
  identity plane never notices (the agent redials its relay by key);
  `nix run .#apply` dials the *overlay* address and needs the laptop on
  nebula.
- 2026-09-19 — **Talos does NOT restart an extension service when its
  ExtensionServiceConfig document changes** (`apply-config` without
  reboot registered `p0agent` v1 but the running container kept its old
  mount namespace). `talosctl service ext-p0agent restart` picks the
  file up. An upgrade/reboot starts it with the file present.
- 2026-09-19 — **Re-serving a machine config without the mesh, one
  browser click**: `curl -X POST …/device/code -d client_id=talos-pxe
  -d mac=<mac> -d uuid=<uuid>` → open `/status?user_code=…`, approve
  (one wallet signature) → poll `POST /token` with the `device_code`
  → `GET /config?mac=…` with the bearer → `talosctl apply-config`.
  The boot token inside is good for **1 h** from serve; the agent
  enrolls within a second of seeing the file, so upgrade first, then
  serve. Fully headless would need two CLI wallet signatures (SIWE
  login + approval nonce).
- 2026-09-19 — **`p0agent` extension chain now**: static agent from
  `nix build --store ssh-ng://mar@nixos --eval-store auto
  .#packages.x86_64-linux.config-server-static` (`bin/nodeagent`,
  ~24 MB; `scp` it over — `file` is not on the box, fish shell), then
  `gh auth token | docker login ghcr.io -u marnyg --password-stdin`
  and `talos/extensions/p0agent/build.sh <bin> <ver>`; pin tag +
  `crane digest` in `talos/hardware/minipc.yaml`. A scratch rootfs has
  no CA bundle: Go's HTTPS client needs the `/etc/ssl/certs` bind the
  0.1.1 spec adds (iroh's relay client carries webpki roots itself).
- 2026-09-19 — `talosctl upgrade --wait` on cp1 takes ~11 min (drain
  with w1 down) and this shell aborts long foreground commands;
  background it (`> /tmp/cp1-upgrade.log &`) and poll `get extensions`.
