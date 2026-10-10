# Exploration Log

<!-- "What we've tried and ruled out." Prevents re-attempting dead ends across sessions.
     Granularity: strategy-level pivots only. Not "used ripgrep instead of sed".
     Yes: "tried library X, ruled out for reason Y." -->

## Mesh v3 — outcome (2026-08-17 → 2026-09-21)

The plan in `docs/mesh-v3-iroh.md` was written 2026-08-17 and
deferred as migration-for-elegance; picked up 2026-09-03 when the
sovereign-actor build made its four components (transport, member
cert, gateway, device apps) work we were doing anyway (ADR-0016).
Gate 2026-09-16, Phases 1–4 2026-09-17 → 09-21, no kill criterion
fired. Strategy-level lessons, so the next migration does not re-learn
them:

- **Gate on a spike with kill criteria, then dual-plane, then
  per-consumer cutover, then event-based soak, then delete** — the
  mesh-v2 recipe held a second time. Every phase left the system
  working; nothing was rolled back. The soak exit was event coverage
  (one re-seal, one reboot, one remote-media session), not a calendar.
- **Tried: the cluster endpoint on the mesh (mesh-v2's step).**
  Reversed at P2.5 — k8s is IP-native and leaves the mesh onto
  declared static LAN addresses; invariant 4's accepted wart closed
  instead of being ported. Cost: an SA-issuer rotation that bounced 14
  control-loop pods (`etzl`), and a router DHCP pool we deliberately
  do not edit (`ebis`).
- Ruled-out alternatives that an ADR now records are pruned from
  here (2026-10-04): the factory schematic (ADR-0023), the iroh
  sidecar (ADR-0021), TCP bridges (ADR-0025; SOCKS/PAC was considered
  and never built), receiver-side policy tables (ADR-0017), the
  ephemeral policy overlay (ADR-0014's 2026-09-20 revision, `ri3b`),
  and n0's relays / QAD (ADR-0022).
- **Not proven on the new plane:** 4K playback / throughput through
  the relay (only P0.2 spike figures), and the parents' TV in the
  field (`4te`). The nebula-era measurements live in ADR-0006.

## Mesh v3 P0.2 — Android (2026-09-16)

- 2026-09-16 — Tried measuring the ≥ 80 Mbps throughput check with
  owner, phone and Mac away from the home LAN. Ruled out: with QAD off
  (ADR-0022) neither side learns a public `ip:port`, so no WAN punch is
  attempted and every session is the fly relay (~50–75 Mbps to a
  phone). Landed on: relay figure recorded as a relay measurement;
  LAN-direct measured at home the same evening on the phone (97 Mbps
  avg, PASS) — the Shield was not needed for the bar and gets its turn
  in the parents'-TV deployment (`4te`).
- 2026-09-16 — Suspected iroh cannot enumerate interfaces inside an
  Android app (netlink restrictions ≥ API 30) and plumbed
  `LinkProperties → AddExternalAddr`. Ruled out as the cause: iroh
  listed Wi-Fi, cellular and tun addresses itself on Android 13. The
  plumbing stays as belt-and-braces; the empty `peer-direct` on the
  node was "nothing validated", not "nothing advertised".
<!-- 2026-09-16: §P0.1 (relay) and §P0.3 (Talos extension) pruned —
     resolved by ADR-0022 and ADR-0023. 2026-10-04: §P0.2's QAD-on-the-
     scratch-relay (ADR-0022, `0pq`), w1-stand-in (spike-only, stale) and
     cp1-install-image (ADR-0023) entries pruned. Recover from git
     history if needed. -->

## Unattended Windows guest on KubeVirt (2026-08-11→14)

- 2026-08-12 — Tried scripting the "Press any key to boot from CD" EFI
  prompt with a VNC keypress (vncdotool, reinstall script v1). Ruled
  out: timing window, requires VNC reachability from the operator's
  machine, not derivable in-cluster. Landed on: repack the ISO with
  Microsoft's own `efisys_noprompt.bin` as the El Torito EFI entry —
  the prompt never exists, reinstall becomes a pure API act.
- 2026-08-13 — Repack v1 extracted the ISO with xorriso. Ruled out: the
  stock ISO is UDF-bridge and the ISO9660/Joliet view xorriso reads
  cannot represent the >4GB `install.wim` — extraction died partway.
  Landed on: 7z extracts the UDF layer; xorriso stays as the rebuilder.
- 2026-08-12 — containerDisk delivery of the ISO via the ImageVolume
  path ruled out: needs k8s ≥1.35 (kubevirt#17460); feature gate
  disabled, ISO delivered by CDI DataVolume import instead.
- 2026-08-11 — Block-mode system disk ruled out: CDI's importer runs
  non-root and cannot open the raw device on a Block-mode Longhorn
  volume. Filesystem mode; revisit only alongside the virtio switch.

## Mesh v3 P2.2 — hub re-learning members after its own restart (2026-09-20)

- 2026-09-20 — Considered the node watching its **home-relay
  connection** (`iroh-ffi` `WatchHomeRelay`) as the "hub died" signal:
  the relay is the hub process's child, so a redeploy restarts it.
  Ruled out by spike: the 1.1.0 binding's `watch_*` methods are sync
  fns that `spawn` outside the tokio runtime (panic: "no reactor
  running", `watch.rs:85`) *and* map `RelayStatus` to `url()` only,
  dropping `is_connected()`; `Addr().RelayUrl()` never blinks across a
  relay restart either. Fixing needs a nix patch + Go regen. Landed
  on: the pooled QUIC connection the beat leaves — iroh keep-alives
  it (5 s), `Closed()` fires 32 s after the hub is SIGKILLed (child-
  process probe, relay up or down). Decision `z2go`.
- 2026-09-20 — Considered persisting the hub's witnessed-member cache
  on a fly volume, a shorter `DefaultBeat`, and the relay access hook
  (`5gz`) reporting connected NodeIds. Ruled out: fungibility (`gar`)
  / brute force / ids-not-names respectively. `pu9q`'s rejections
  (short `/.well-known` polling, hub push channel) carry over; the one
  poll admitted is against a *sealed* hub, bounded to the sealed
  window.

## Mesh v3 P2.1 — admin CLI paths on the tun (2026-09-19)

- 2026-09-19 — Considered `nodes: [cp1]` / `-n cp1.mesh.internal` /
  `-n 127.0.0.1` for talosconfig. Ruled out from Talos source: apid on
  a control plane dials every `-n` target as `<target>:50000` with SNI
  (never local), so the target must be self-resolvable *and* a SAN —
  only the hostname is. Considered pinning `hostname: cp1` live.
  Ruled out: renames the k8s Node on the only control plane and
  orphans Longhorn's single-replica volumes bound to `talos-wu6-eib`
  (`t7b2` at reinstall). Landed on: the generated hostname, honestly.
- 2026-09-19 — Considered leaving `nodes:` empty so the endpoint is
  the node. Ruled out: talosctl refuses nearly every command without
  nodes.
- 2026-09-19 — Considered `apply` dialing each node by
  `<name>.mesh.internal`. Ruled out: needs every node on the identity
  plane (w1 is not). Landed on: the control plane's mesh name as the
  endpoint, `-n <hostname>` proxied by apid via `resolveMemberNames`
  — the talosconfig shape.
- 2026-09-19 (later, `qb5q`) — **The constraint behind the `apply`
  ruling above has lifted**: w1 now runs the agent, so every node is
  on the identity plane and per-node `<name>.mesh.internal` dialing is
  viable. Not re-opened — the apid-proxy shape still has the smaller
  blast radius (one endpoint, works for any future agentless node, and
  matches talosconfig) — but the "ruled out" reason is no longer the
  reason. Re-read this before assuming the option is closed.
- 2026-09-19 — Considered putting the hub in the name map so
  `hub.mesh.internal` resolves like a member. Ruled out: the map is
  witnessed member certs and the hub holds none; a synthetic entry
  would make the Issuer a member of its own network. Landed on: the
  daemon resolves `nodeagent.HubName` from the hub record it already
  keeps for the beat.

## Mesh v3 P2.4 — the app's build host (2026-09-20)

- 2026-09-20 — Considered keeping the Android APK in CI now that the
  app links iroh. Ruled out for now: the AAR needs
  `libiroh_ffi.a` for `aarch64-linux-android`, and that build is impure
  (unfree NDK) and ~1 h cold — a stock runner would pay it on every
  run. Landed on: build on the NixOS box (`android/shell.nix`),
  `android/publish.sh` to the same rolling release, workflow
  dispatch-only. Revisit if the cross build can be pushed to a binary
  cache CI can read — the rest of the workflow (gradle, release upload)
  still works as written.
- 2026-09-20 — Considered adopting the P0.2 spike's `iroh-go/mobile`
  package as the app's core (it already did netstack + fake IP + iroh).
  Ruled out: it is a one-peer spike with no certs, no name map and its
  own copy of the fake-IP dialect. Landed on: the app binds
  `config-server/mobile` over the same `nodeagent` + `meshtun` the
  desktop daemon runs, so there is one zone rule and one pool; the
  spike package is superseded and can be deleted.
## In-cluster gateway statefulness (2026-10-01)

- 2026-10-01 — Considered an ephemeral-key in-cluster gateway (no
  PVC, fresh key + headless re-enrollment on every reschedule) to end
  the RWO-volume node pin (`9l67` slice 3). Ruled out (decision
  `nfmt`): the two things that made the stateful gateway hurt were
  fixed elsewhere — member runway 3.5 d → 90 d (`5hek`) and Longhorn
  never releasing a dead node's volume (`nodeDownPodDeletionPolicy=
  delete-both`) — leaving ~30–60 s of `*.gw` downtime per node loss,
  which is accepted. Invariant 2's `359.9.3` placement stands. Revisit
  only if gateway failover must be sub-10 s or if the gateway becomes
  multi-replica (then the per-pod key question returns as `4ze8`'s).

## Interrupting a blocked stream read from Go (2026-10-03, `vzbf`)

- 2026-10-03 — Tried `RecvStream.Stop` from `Raw.Close` to unblock a
  concurrent `Read`. Ruled out: iroh-ffi 1.1.0 holds one tokio
  `Mutex` across `read().await` and `stop()`, so `Stop` queues behind
  the read it is meant to end (deadlock). uniffi's
  `rust_future_cancel` is not emitted by uniffi-bindgen-go either.
  Landed on: never abort a direction with a call in flight; reset the
  idle send side so the peer ends the stream; destroy by whichever of
  `Close`/last-call is later (`c4a6414`). Real cancellation is an
  iroh-ffi patch (`vh6e`).

## Sonarr/Radarr through Jackett (2026-10-03)

- Tried Jackett's `/api/v2.0/indexers/all/results/torznab/` as one
  Torznab entry. Ruled out: Sonarr v4 and Radarr reject it by name
  ("please add indexers individually"). Landed on one entry per
  Jackett indexer id, the list shared via `jackett-config`.
- Probed 11 public trackers from the cluster. 1337x and eztv: Cloudflare
  challenge, needs FlareSolverr. yts, bitsearch: redirect to a new
  domain. nyaasi, therarbg: error/403. limetorrents, torrentproject2,
  torrentdownloads: 0 TV results. Landed on thepiratebay (apibay) and
  knaben (meta-search). Re-probe before adding others; they rot.

## Agent workspaces — where the git credential lives (2026-10-11, `ivof.3`)

Question: a child agent needs write access to a repo; is that token a
Kit concern, a `#task` concern, or neither? Ruled **same class as the
OpenRouter key** (a data-plane credential, glossary): the driver
provisions it at spawn, child scope = token scope, one lease.

- Ruled out **token in the starter kit**: the kit is `{grants,
  locations}` (`protocol/spawn/spawn.go:262`) — chains only; a bearer
  token would be a second authority primitive beside the delegation
  cert (protocol invariant 1) and would make the parent hold git
  credentials for every child.
- Ruled out **token in the `#task` envelope**: the parent becomes a
  token distributor, the secret sits in a signed body that can leak
  into a transcript or reply, and a long-lived child accumulates
  credentials for repos it was not born for.
- **Deferred, not ruled out: Forgejo deploy keys** — the child mints an
  SSH keypair at birth (protocol invariant 7), the parent registers the
  pubkey as a write deploy key and removes it at `Kill`/deadline; no
  secret travels. Needs a Forgejo-aware parent and parent-side cleanup
  (deploy keys do not expire). The endgame after `e6pu`.
- Ruled out **the parent naming the credential in the spawn request**:
  `#spawn` is `{image, params, until}` and `params` is opaque to the
  provisioner (it becomes the intro); a parent that can name namespace
  secrets reads any secret in `ai` through a child. Landed on
  **provisioner policy keyed on image** (driver `Config`, ops-side):
  `sandbox-agent` ⇒ `{openrouter, forgejo-agent}`. Accepted cost:
  scope = the token's scope, so v0 is one token for an `agents` org and
  "a child cannot be re-pointed" holds per org, not per repo, until
  deploy keys.

### Result channel and sessions (same session)

- Pinned: a task's result is a **Forgejo branch + PR**; the
  swarm-worker report is the PR body; the `#task` reply is a pointer
  `{status, branch, head, pr}` (invariant 12: a reply carrying the only
  copy of the result is a result that can be lost). Human merges in
  Forgejo for v0. Ruled out: report inline in the reply; report as a
  file on the branch (pollutes review); BLOCKED without a push.
- **Superseded within the session: git as the session store**
  (`agents/sessions` repo, `pi --session-dir`, push on timer + SIGTERM).
  Right for durability and resume, wrong once "watch a run live from
  the phone" and "search every transcript" became goals \u2014 git streams
  nothing and indexes nothing.
- Ruled out **a standalone session service**: it is the parent actor's
  born table with more columns, and the ingest boundary (pi events over
  a stream) would be built twice \u2014 plain HTTP now, facethttp at
  `e6pu`. The backend *is* the parent (spawner + store + UI behind the
  group gate); not the orchestrator `6d0u`/ADR-0036 rejected, which was
  third-party reconciliation.
- Ruled out **Postgres for the parent's store**: single writer, single
  owner, no HA anywhere else (`4ze8`); SQLite+FTS5 on a Longhorn PVC
  in the parent's manifest. First actor with a durable datastore \u21d2
  `bsj` (backup target) blocks the epic.
- Ruled out **a storage provider actor** ("bring your own provider" for
  state): BYO provider already *is* the compute design (invariant 13,
  ADR-0009, two drivers, payment deferred to M5 by acting as our own
  provider); a volume has nothing to hand over the mesh, so storage is
  a lease dimension, deferred as `264f` under ADR-0005's
  consumer-driven rule. This iteration uses plain primitives.

### Ingest (same session)

- Pinned: **the child beat runs pi as an RPC subprocess** (`pi --mode
  rpc --session-dir …`): the `#task` prompt goes down as a `prompt`
  command, every stdout record goes up to the parent, `steer`/`abort`
  come down from the parent's UI, deadline/`#kill`/lapse = close stdin
  (pi's orderly shutdown). Two artifacts: the event stream (live tail,
  search) and pi's own session JSONL shipped at settle (resume/fork).
- Ruled out **a pi extension pushing to the beat** (herdr's pattern):
  observe-only, needs a second invented channel for control, runs
  inside the sandboxed process. Ruled out **tailing the session file**:
  racy on an append-in-progress file, no structured events, no
  control. The TUI-in-tmux entrypoint stays as a debug path only; it
  cannot share a pi process with the RPC path.
- Pinned **sharing**: the parent's web UI sits behind the **person
  gate** (OIDC client of `siwe-oidc`, like ArgoCD) because a session
  list with owners and shares is per-user state (ADR-0032's own
  invalidation clause for the group gate). Owner = the wallet that
  submitted the task; a share list of wallets/groups at two levels,
  `view` and `steer`; `admins` views all; nothing public; enforced in
  the parent on every request and stream subscription; steers carry
  the sender's wallet into the transcript. The power-user terminal is
  a client of the parent's stream under the same ACL (later);
  `kubectl exec` stays the debug door. Ruled out: group gate + ACL by
  bare `X-Mesh-Groups` (ADR-0032 forbids it); device-level sharing
  (`7ymy` makes it moot anyway); finer rights than view/steer (the
  orchestrator question again).

### Forgejo (same session)

- Pinned: **person gate** (`-client=forgejo=\u2026` on `siwe-oidc`, OIDC auth
  source with auto-registration), **not** `-token-client` \u2014 a review by
  "laptop" is wrong, by the person is right (the Jellyfin reasoning).
  No `auth-url` on the Ingress: Forgejo gates itself, because git
  clients carry no cookie (ADR-0032 ruling 1 gives a 401 with no
  redirect) and the agent path is pod-network (`forgejo.forgejo.svc`).
  Humans: PAT in the laptop's credential helper; agents: the
  org-scoped token sealed in `ai`. SSH off for v0 (`gw` is HTTP-only).
- Own namespace `forgejo`, raw manifests, sqlite3 backend on one
  Longhorn PVC (single owner; Helm chart only if raw turns ugly), host
  `git.gw.mesh.internal`. Orgs: `agents` (writable by the agents
  token), `mirrors` (pull-mirrors of GitHub, read-only). Working on
  `talos-config`: agent branch on `agents/talos-config` (fork of the
  mirror) \u2192 owner fetches, merges **signed** on the laptop, pushes to
  GitHub; the Forgejo PR is review-only (decision `4la8`, signed-tip
  rule intact).
- Ruled out: group gate in front of Forgejo (breaks every non-browser
  git client); Postgres (no second owner, no HA elsewhere); SSH on
  the mesh for v0 (a second port on `gw`, nothing agents need).

## Spike round 2026-10-06 — app seam, HTTPS, agents

Three memos (`docs/spikes/`), rulings on the beads. Strategy-level
dead ends so they are not re-walked (the app-seam entry — bare header
vs signed token — moved into ADR-0032 when it was accepted; the
agent-runtime entries — ax, kagent, gVisor, Firecracker — into
ADR-0035):

- 2026-10-06 — Considered **a wallet-rooted mesh CA** for HTTPS on
  `*.gw.mesh.internal` (the deferred `goals.md` line since 07-31).
  Ruled out both ways: a seed-derived CA key contradicts ADR-0018 ("the
  seed is never a signing key"); a `speak-as` ephemeral-key CA rotates
  the trust-store root every hub redeploy. Also: the member's Ed25519
  key cannot be a browser TLS leaf; nginx/per-app termination loses
  ADR-0026's header injection. Landed on: nothing until an app forces
  it, then **device-local termination + name-constrained per-device CA**
  (the daemon that already fakes DNS/IPs fakes the server too).
