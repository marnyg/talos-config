# Mesh v3 — identity-native networking (iroh) replaces nebula

_Design record + migration plan, 2026-08-17. Status: **deferred by
decision — do not build**. Product of a design conversation exploring
whether iroh-style identity addressing fits this project better than
an IP overlay. Conclusion: the architecture is coherent and has no
known dead ends, but there is no operational driver today; the plan
exists so it can be picked up when a trigger fires (see §Pickup
triggers). Nothing here is scheduled. Written against the repo as of
mesh-v2 completion (ADR-0007, ADR-0009, ADR-0013, ADR-0015)._

## Premise

Mesh v2 (nebula, `mesh-v2-nebula.md`) is complete and healthy. This
is not a repair; it is a re-founding of the transport on
**identity-addressed networking**: peers are dialed by public key
(iroh NodeId) over QUIC, not by overlay IP. The question it answers:
how much of the current mesh's complexity is *networking* and how
much is *IP bookkeeping* — and what the system looks like if the
bookkeeping is deleted.

## The analysis this plan rests on

### Fundamental vs incidental IP

| Mesh consumer today | IP is… | Consequence for v3 |
|---|---|---|
| kubelet → apiserver (worker join) | fundamental to k8s, **incidental to the mesh** (nodes share a LAN; mesh endpoint was chosen to escape DHCP drift, invariant 7) | k8s leaves the mesh: static LAN addressing declared in git |
| talosctl / kubectl | incidental-ish (third-party binaries wanting a socket) | local TCP bridge over an iroh stream (dumbpipe pattern) |
| Hub → node dials (bootstrap probes, /status) | incidental (own code; hub already fakes IP via gvisor netstack because fly has no TUN) | goes identity-native; netstack machinery deleted |
| Browser UIs (ArgoCD, media, SIWE flows) | fundamental (browsers are IP+DNS+TLS clients) | served via fake-IP TUN presentation on devices (below) |
| Jellyfin apps on phones/TV | fundamental (third-party apps dial URLs) | same fake-IP TUN presentation |
| `mesh.internal` DNS server | pure IP overhead | deleted; name→NodeId map is a pure function of git, shipped to clients |
| Overlay CIDR, cert-baked addresses, certSAN IPs | pure IP overhead | unrepresentable in v3 — identity is the address |

Key insight #1: **an IP overlay is a single universal
legacy-adapter** (the TUN) that makes all third-party software work
unmodified. Identity networking deletes the bookkeeping but pushes an
adapter to every edge where third-party software lives. v3 is worth
it only because those adapters double as components the
sovereign-actor design needs anyway.

Key insight #2: **IP survives only as device-local fiction.** The
client TUN answers DNS for `*.mesh.internal` with synthetic IPs
(fake-IP mode, `198.18.0.0/15`) minted per device, per session —
never coordinated, never in a cert, never in git. The fleet-wide
shared artifact shrinks to *name → NodeId*.

### What v3 buys / what it costs

Buys:
- Deletes: overlay CIDR + address allocation, mesh DNS server,
  cert-baked IPs, hub gvisor-netstack embedding (`nebstack`), the
  vendored nebula service package (ADR-0005), renumbering as a
  concept.
- Per-request cryptographic device identity at the gateway
  (successor to ADR-0007's cert-group + source-IP inference).
- Native alignment with the sovereign-actor sketch
  (`protocol/docs/sovereign-actor-protocol.md`): NodeId ≈ actor identity, relays
  and lighthouses become ordinary self-hosted services, ALPN ≈ facet
  classes.
- Kills the accepted wart in invariant 4 (lighthouse as hard
  dependency of cluster membership) — k8s leaves the mesh.
- On mobile: iroh core needs no TUN; the VPN slot is spent only on
  the *presentation* layer we choose to ship.

Costs:
- Four bespoke components owned forever: node agent (Talos
  extension), in-cluster gateway, desktop daemon, Android/TV fake-IP
  VPN app. No upstream Sidero extension, no official mobile app.
- iroh is young (1.x since 2026-06, semver-committed — see P0.4 below;
  was "pre-1.0" when this was written), a startup's roadmap, must run
  with n0's hosted discovery/relay infrastructure fully disabled; Go
  bindings are community-only.
- Reverses one piece of mesh-v2 phase 2 (cluster endpoint back to
  LAN, now static).
- Not reversible in an afternoon.

## Architecture (end-state)

```
hub process boots → hubkey (Ed25519, random per process)
wallet signs speak-as {aud: hubkey, cav: {verbs, groups}, exp 120 d}   (= unseal, ADR-0018)
  → hubkey signs member certs + invoke grants; bundle carries the speak-as
  → hubkey is also the relay+gateway identity for this process
wallet sig over frozen message → HKDF secrets seed (masterderive)
  → age identity, KMS seal keys, recovery passphrases ONLY — never a signing key
member NodeIds: Ed25519 keypairs minted ON the member (ADR-0015
pattern), never derived, never in transit. Membership = a
wallet-authorized cert binding NodeId → name + groups + expiry,
signed by hubkey and resolved to the wallet through the speak-as.
Verifiers hold no CA: their own consent grant is the root.
_(Was: HKDF master → issuer key; replaced 2026-09-06 by ADR-0018.)_
```

- **Hub (fly.io)**: config-server + KMS + enrollment (unchanged) +
  **self-hosted iroh relay** (single public entrypoint preserved:
  HTTPS 443 carries the relay protocol; QUIC direct on the existing
  UDP port). No n0 infra anywhere: default discovery disabled, hub
  relay pinned as home relay in every member config. Hub also serves
  the signed name→NodeId map (successor of the DNS zone; pure
  function of git).
- **Nodes**: a Talos system extension we build — a small agent
  holding the node's NodeId, accepting ALPN-gated streams and
  forwarding to loopback targets per policy (apid :50001,
  kube-apiserver :6443, ingress :80, Jellyfin NodePort). Outbound
  dials to the hub relay only. This replaces the nebula extension.
- **In-cluster gateway**: iroh listener pod terminating identity
  streams and forwarding to Services — the mesh side of ingress
  (revises ADR-0009). Injects a verified per-request device-identity
  header (revises ADR-0007). Its NodeId enrolls like any member.
  **Authorization rule (decided 2026-09-03, `359.9.3`):** the gateway
  authorizes at the *network layer* only — caller's cert chain ×
  git policy; no per-session login; device custody is access for the
  cert lifetime. User sessions stay at the *app layer* on the
  SIWE→OIDC bridge (ADR-0010). The header **complements** SIWE, it
  does not replace it. Owner-only actions are requests signed by the
  wallet itself; there is no "presence"/"freshness" concept.
- **Desktop** (`irohup`, successor to `cmd/nebup`): daemon exposing
  (a) TCP bridges for talosctl/kubectl, (b) either SOCKS/PAC or the
  same fake-IP TUN as mobile for browser traffic. Enrollment flow
  unchanged: wallet signs, hub authorizes the locally-minted NodeId.
- **Android/TV app** (evolves the existing `android/` app,
  ADR-0013): keeps `VpnService`, drops the nebula AAR. TUN → gvisor
  netstack → fake-IP DNS for `*.mesh.internal` → per-flow iroh
  streams. Split routing: only synthetic range enters the tunnel.
  Jellyfin app and browser work unmodified with normal hostnames —
  distinct origins, working cookies, OIDC redirects intact.
- **k8s / Talos control plane**: off the mesh. Static LAN IPs
  declared in `talos/machines/<mac>/` (git-derived, satisfying
  invariants 2 and 7 — declared, not leased; requires DHCP pool
  exclusion on the router). certSANs carry LAN name/IP. Remote
  kubectl/talosctl ride the desktop daemon's bridges.
- **Policy**: `talos/mesh-policy.yaml` survives as the who×what
  table; "what" changes from ports to **facets** (ALPN classes with
  producer-side forward targets). ~~Compiled into gateway/agent
  accept rules instead of nebula firewall stanzas.~~ **Revised by
  ADR-0017 (2026-09-03):** the recipe compiles into `invoke` grants
  that *callers* fetch and present; receivers hold only their own
  accept table (`facet → forward`) and a consent grant, never a policy
  table. Blocklist (`mesh-blocklist.txt`) becomes NodeId-based; expiry on membership certs finally gives the
  revocation story thread dc04e3e8 wanted (nebula has no CRL; certs
  with short expiry + renewal beat do).

### ALPN ↔ facet mapping (rules, from the actor-design review)

1. ALPN discriminates **protocol + version + trust class** only
   (e.g. `mesh/apid/v1`, `mesh/http/v1`, `mesh/frontdoor/v1`) —
   coarse classes, not fine-grained verbs. Fine dispatch stays in
   the (signed) application layer.
2. **ALPN is unsigned — it routes, it never authorizes.** After
   accept, the receiver runs `authorize()` (domain-model glossary,
   ADR-0017) over the caller's presented bundle — `member` cert +
   `invoke` grants — rooted in the receiver's own consent grant.
   Mismatch between ALPN facet and the granted facet = drop.
3. ALPN is visible in the QUIC ClientHello (relays and on-path
   observers see it). Coarse classes bound the metadata leak.

## Invariant compliance (desired-state/invariants.md)

| # | Verdict |
|---|---|
| 1 stateless identity/membership | Holds, same shape as today: keys minted on members, membership = wallet-derived-issuer cert, set bounded by wallet-signed acts, re-derivable minus member private keys |
| 2 git single source of truth | Holds: name→NodeId map, policy, static LAN IPs all git-derived; hub remembers nothing |
| 3 owner-held roots | Unchanged |
| 4 mesh is post-bootstrap | **Strengthened**: k8s membership no longer depends on the lighthouse/relay; provisioning stays HTTPS + device flow |
| 5 single public entrypoint | Holds: hub HTTPS (now also relay protocol) + existing UDP port for QUIC |
| 6 hardware-selected, human-ratified config | Unchanged |
| 7 no ephemeral facts in durable identity | Holds; static LAN IPs are declared config, and overlay addresses cease to exist |
| 8 secrets in memory only | Unchanged |

## Migration plan

Modeled on mesh-v2: gate on a spike, run dual planes, cut over
per-consumer, delete. Every phase leaves the system fully working;
nebula is not touched until phase 4.

### Phase 0 — spike gate (~2–4 days). Fail ⇒ whole plan shelved again.

All on scratch infra; no repo changes beyond a spike branch.
1. **Self-hosted relay on fly**: iroh relay in the hub process (or
   sidecar), 443 + UDP; two peers behind different NATs connect via
   relay and hole-punch LAN-direct when co-located. **All n0
   endpoints disabled and verified absent** (no DNS discovery, no
   default relays — packet-capture check).
2. **Android feasibility**: iroh (FFI/gomobile) inside a
   `VpnService` with gvisor netstack fake-IP; Jellyfin app streams a
   4K remux ≥ 80 Mbps sustained through it (kill-criterion parity
   with mesh-v2 #3), acceptable battery over a 2h stream.
3. **Talos extension proof**: minimal agent as a system extension —
   boots, dials the hub relay outbound, forwards one inbound
   ALPN-gated stream to apid; survives `talosctl reboot`.
4. **API-churn probe**: pin iroh version; note breaking-change rate
   over the spike window and the upgrade cost of one version bump.

### P0.4 API-churn probe (2026-09-06)

The premise of kill-criterion 4 ("iroh is pre-1.0") **no longer holds**:
iroh shipped **1.0.0 on 2026-06-15** and is now **1.1.0 (2026-08-25)**,
with a public semver commitment for the `1.x` line.

| version | date | breaking? — what |
|---|---|---|
| 0.96.0 | 2026-01-28 | yes (~13) — pre-1.0 endpoint/discovery churn |
| 0.98.0 | 2026-04-17 | yes (~8) — pre-1.0 API reshaping |
| 1.0.0-rc.0 | 2026-05-07 | yes (~15) — the big pre-1.0 cleanup |
| **1.0.0** | 2026-06-15 | yes (2) — relay `CaTlsConfig` rename; 1.0 deps |
| 1.0.1 | 2026-06-29 | no — bugfix only |
| 1.0.2 | 2026-07-06 | 1 — minor serialization fix |
| 1.0.3 | 2026-07-20 | no — bugfix only |
| **1.1.0** | 2026-08-25 | 1 — `CustomAddr` wire-serialization fix (unstable transport) |

Cadence: ~20 releases in the last 12 months (roughly a minor every
3–4 weeks pre-1.0). Breaking surface was almost entirely the
`Endpoint`/discovery/relay-config APIs. **Post-1.0 the breaking rate
dropped ~10×** (2→0→1→0→1) and remaining "breaking" tags are wire/
unstable-transport fixes, not load-bearing `Endpoint` API changes.

**Bindings (`iroh-ffi`, uniffi):** alive and tracking core — v1.1.0
released 2026-07-16, repo last pushed 2026-08-18. First-party bindings
ship for **Python, Swift, Kotlin/JVM (Maven `computer.iroh:iroh`),
Node.js**. Kotlin/Android is therefore well-supported first-party.
**Go is NOT first-party** — it is a *community-maintained* uniffi-Go
binding (`git.coopcloud.tech/decentral1se/iroh-go`), currently pinned
to iroh-ffi v1.1.0 / core v1.0.3 (one patch behind). This is the
single real risk for our three **Go** embeddings (hub, Talos ext,
desktop). Mitigation: generate Go bindings in-house from iroh-ffi via
`uniffi-bindgen-go`, or fall back to a Rust sidecar — do not take the
community binding as a load-bearing, unforked dependency.

**Self-hosting / offline mode:** fully supported. `iroh-relay` is a
first-party binary in the main repo (1.1.0) with bearer-token access
control "without an external service", custom `ServerCertVerifier`,
and Let's-Encrypt TLS. The `Endpoint` builder supports
`RelayMode::Disabled` and `Endpoint::empty()` ("no address lookup
services"), so a node runs with **only a custom relay and zero n0 DNS
discovery / pkarr**. This corroborates P0.1.

**Cost of one version bump (three embeddings), post-1.0:**
- Go surfaces (hub + Talos ext + desktop) share one binding: regen +
  fix 1–2 signatures + retest ≈ **4–6 h total** (they move together).
- Android/Kotlin (`computer.iroh:iroh` bump + AAR rebuild + smoke) ≈
  **1–2 h**.
- Total ≈ **~1 engineer-day per quarterly bump** (was 2–3 days pre-1.0).
  Well under the "½ day each on two consecutive bumps" fail threshold.

**Pin:** start Phase 1 from **iroh / iroh-relay 1.1.0** and **iroh-ffi
1.1.0** (Kotlin/Android from Maven `computer.iroh:iroh:1.1.0`; Go via
in-house `uniffi-bindgen-go` off iroh-ffi 1.1.0). Rationale: latest
stable, first past the 1.0 stabilization so the surface has settled,
and ffi/core versions are aligned.

**Verdict: PASS-WITH-CONDITION** against kill-criterion 4. Condition:
own the **Go** binding generation (uniffi-bindgen-go in-house or Rust
sidecar) — do not depend on the community `iroh-go` as an unforked
load-bearing dependency. The pre-1.0 churn premise is retired; also
satisfies the "iroh reaches 1.0" pickup trigger.

Sources: crates.io `iroh`/`iroh-relay`/`iroh-base` (max 1.1.0);
github.com/n0-computer/iroh releases (v0.96.0…v1.1.0);
github.com/n0-computer/iroh-ffi (README + releases v1.0.0/v1.1.0);
central.sonatype.com/artifact/computer.iroh/iroh;
git.coopcloud.tech/decentral1se/iroh-go (README version-support);
docs.rs/iroh/latest `endpoint::Endpoint`/`Builder` (`RelayMode::Disabled`,
`empty()`).

### Go binding: bindgen vs sidecar — data (2026-09-06)

Task `talos-config-ow7`, the P0.4 condition. Pipeline lives in
`iroh-go/` (own `go.mod`; `protocol/` untouched), flake outputs
`packages.{iroh-go,iroh-go-smoke,iroh-ffi,iroh-relay,uniffi-bindgen-go}`,
`apps.iroh-go-regen`. Bindgen produced a compiling, working Go package
well inside the time-box, so the sidecar proof was **not** built.

| question | data |
|---|---|
| pins landed | iroh-ffi **1.1.0** (git tag; not on crates.io) → its `Cargo.lock` pins **core iroh 1.0.2**, taken as-is. uniffi 0.31.2 in the ffi ↔ **uniffi-bindgen-go 0.7.1+v0.31.0** (uniffi 0.31.0) — same minor, contract-version + checksum check passes at `init()`. iroh-relay **1.1.0** from the core repo tag (nixpkgs has 0.95.1). |
| did it generate? compile? | yes / yes after **3 textual fixups** (`iroh-go/regen/fixup.sh`): `HashMap<Vec<u8>,_>` → invalid `map[[]byte]T` (one field); `IrohError.Error()` by value returning `"IrohError"` (vet copylocks); package clause ignores `package_name`. No generator patch needed for async methods/constructors, foreign-implementable trait interfaces (`Preset`, `ProtocolHandler`), error objects, records, enums, `Display/Eq/Hash`. Bindgen also insists on `cargo metadata` in cwd → a dependency-free dummy crate satisfies it offline. |
| smoke: direct | **PASS** — two `Endpoint`s, `PresetMinimal` (no n0 DNS/pkarr/relays), `RelayMode::Disabled`, dial B's bound `127.0.0.1` socket, ALPN `mesh/smoke/v1`, one bi-stream echo, clean close. Selected path `*ip:127.0.0.1:<port>`. |
| smoke: custom relay | **PASS** — `iroh-relay 1.1.0 --dev` on loopback, `RelayMode::Custom(http://127.0.0.1:<port>)`, B dialed by id + relay URL only. Selected path `*relay:http://127.0.0.1:<port>/`, rtt 1 ms. (core 1.0.2 client ↔ relay 1.1.0: wire-compatible.) Both run in `nix build .#iroh-go-smoke`'s checkPhase and in `go test ./...`. |
| build time | nix cold (aarch64-darwin, M-series): iroh-ffi **7–10 min**, iroh-relay **8 min**, uniffi-bindgen-go **~45 min** (one huge askama crate; built once, cached), bindgen run **1.4 s**, Go build **~2 s**. Warm: Go step only. |
| native lib size | `libiroh_ffi.a` **21.8 MB**, `.dylib` **15.1 MB** (release + LTO, iroh-ffi's profile). |
| Go binary size | smoke **15.0 MB** stripped, Rust archive linked statically; 3.8 MB if dynamically linked to the dylib. |
| CGO required | **yes**, always (uniffi is a C ABI). C toolchain at build time; runtime deps = libc/libSystem (+ macOS frameworks). |
| cross-compile | Go-alone cross-compile is gone: each (os, arch) needs its own `libiroh_ffi.a` + C toolchain → **one builder per target** (`pkgsCross` under nix). **aarch64-linux verified** (nix build in a `nixos/nix` container: both smokes pass, 16.1 MB glibc-dynamic ELF, `NEEDED` = libc/libm/libdl/libpthread/libgcc_s; relay test even hole-punched to direct); **x86_64-linux unverified** (no builder; derivation evaluates). Talos static/musl: `pkgsStatic` path identified, **not attempted**. |
| API ergonomics | `preset := iroh.PresetMinimal(); mode := iroh.RelayModeDisabled(); ep, err := iroh.EndpointBind(iroh.EndpointOptions{Preset: &preset, BindAddr: &addr, Alpns: &alpns, RelayMode: &mode})` — then `ep.Connect(iroh.NewEndpointAddr(id, &relayURL, nil), alpn)`, `conn.OpenBi()`, `bi.Send().WriteAll/Finish`, `bi.Recv().ReadToEnd`. Warts: `Option<Arc<T>>` → `**T`; optional scalars are pointers; async calls block the goroutine; objects have `Destroy()`. Usable as-is; a thin idiomatic wrapper is a later nicety, not a need. |
| version bump cost | pins in `nix/sources.nix` → `nix run .#iroh-go-regen` → drift check + smoke → commit lib pins + generated package together. Measured: core 1.0.2→1.1.0 inside the ffi lock = `cargo update` + 3 min build, **byte-identical Go output**, smoke green. An ffi-surface change adds compiler-guided consumer fixes. Consistent with the P0.4 estimate (4–6 h for the three Go embeddings). |
| nix caveats hit | crates.io now 403s generic User-Agents; the flake's 2026-01 nixpkgs `fetchCargoVendor` has none → two-line backport (UA + `static.crates.io`) in `iroh-go/nix/default.nix`, drop when nixpkgs is bumped. uniffi-bindgen-go's workspace lock has uniffi 0.31.0 from two sources → vendored from a lock trimmed to the `bindgen` member. |

**Recommendation: bindgen, in-house — no sidecar.** The generator
works on iroh-ffi 1.1.0 with three exact-match textual fixups and zero
forks; the whole chain is fixed-output vendored in the flake, the
checked-in package is drift-checked on every build, and both the
relay-less and custom-relay paths pass from Go. A sidecar would add a
process boundary, a bespoke IPC protocol and its own version skew for no
gain that this data shows. The risk I am least sure about is the
**Talos system-extension link**: a fully static musl build of the Rust
archive + cgo Go binary is identified (`pkgsStatic`) but was not
attempted, and the x86_64-linux build itself is unverified here — run
`nix build .#iroh-go-smoke` on an x86_64-linux builder before Phase 1
commits to the extension shape. Second risk: uniffi-bindgen-go is a
single-vendor (NordSecurity) project that tracks uniffi with a lag;
pin it, and treat an iroh-ffi uniffi minor bump as the moment to
re-check the fixups.

### Phase 1 — identity plane beside nebula (dual plane)

- `irohderive`-equivalent: issuer key from `masterderive`; membership
  cert mint/verify (reuse the enrollment flow in `nebenroll.go` /
  `deviceflow` — the wallet-signature UX is unchanged).
- Hub: embed relay + membership issuance + name→NodeId map endpoint.
- Node extension on one node (cp1) via factory schematic +
  `talosctl upgrade` (no wipe).
- Desktop `irohup`: enrollment + talosctl/kubectl bridges.
- **Exit checks** (event-based, not calendar): node reboot →
  agent reconnects unaided; hub re-seal → identity plane reconverges
  after unseal; laptop roams LAN→cellular → relay path holds, LAN
  path re-punches direct.

### Phase 2 — consumers migrate, one at a time (each step reversible)

1. Admin CLI paths (talosctl/kubectl) onto bridges. Nebula still
   carries everything else.
2. Hub→node dials (/status, bootstrap probes) onto identity streams;
   delete the hub's netstack dial path (keep code until phase 4).
3. In-cluster gateway deployed; one low-stakes UI (e.g. Jackett)
   exposed through it end-to-end with the per-request identity
   header; then the rest of ingress (ADR-0009 revision).
4. Android app swaps nebula AAR for iroh+fake-IP internals (same
   APK, ADR-0013 pipeline); phones/TV re-enroll NodeIds via the
   existing device flow. Media verified: LAN-direct and remote-relay.
5. k8s endpoint off the mesh: static LAN IPs into machine configs +
   router DHCP exclusion; certSANs → LAN names; talosconfig/
   kubeconfig re-pointed (reverses mesh-v2 phase-2 step 2; sequenced
   late because it is the only step touching cluster availability).

### Phase 3 — soak

All traffic on the identity plane; nebula idle but installed. Wait
for one natural hub re-seal and one node reboot to pass, plus one
full remote-media session. No calendar minimum — event coverage, the
mesh-v2 lesson.

### Phase 4 — deletion

- Factory schematic without the nebula extension; upgrade nodes.
- Delete: `config-server/mesh/neb*.go`, `nebderive`, `nebstack`,
  `nebenroll.go`, `cmd/nebup`, vendored nebula service pkg, nebula
  AAR build (`android/build-aar.sh` nebula parts), DNS shim in
  `mobile/`.
- ADRs: new ADR "identity-native mesh replaces nebula" superseding
  0002/0005; revisions noted against 0006 (relay-by-default carries
  over verbatim), 0007 (identity header), 0009 (gateway), 0013 (app
  internals), 0014 (policy render targets).
- Update `desired-state/{goals,invariants}.md`, `domain-model.md`,
  `deployed-state.md`; fold this file's outcome into an exploration-
  log entry.

## Kill criteria (any one fires ⇒ stop, keep nebula, close the spike)

1. Spike check 1 fails: relay cannot be fully self-hosted / n0 infra
   cannot be cleanly disabled.
2. Spike check 2 fails: mobile fake-IP path can't hold the 80 Mbps
   floor or wrecks battery.
3. Spike check 3 fails: no viable Talos extension path.
4. Churn burn: two consecutive iroh upgrades each cost >½ day of
   breakage, or a load-bearing API is deprecated mid-migration.
5. During phase 2: any consumer needs a workaround that reintroduces
   fleet-coordinated addressing — that's the design refuted, not a
   bug to fix.

## Pickup triggers (what makes this worth starting)

- Sovereign-actor work moves from sketch to build (dominant trigger —
  the gateway, device apps, and membership certs are its components).
- Nebula ecosystem risk materializes: Slack/Defined stagnation, or
  Mobile Nebula breaking on an Android release (matters even with the
  custom app: the AAR is upstream code).
- A concrete need for per-request device identity that ADR-0007's
  network-layer inference can't serve.
- iroh reaches 1.0 / API stability, removing kill-criterion-4 risk.

## Ruled out (with reasons)

| Option | Why not |
|---|---|
| Localhost-proxy-only clients (no TUN) | Cookie/origin collapse on `localhost:PORT`; every third-party app needs manual pointing; the one thing it saved (VPN slot) is worth less than universal compatibility |
| ALPN as authorization | ALPN is unsigned ClientHello data; routing hint only |
| Fine-grained facet ALPNs | Metadata leak to relays/on-path observers; coarse trust classes only |
| n0-hosted relays/discovery | Third-party infra in the connectivity path; violates invariants 3/5 in spirit |
| Public hub HTTPS reverse-proxy for remote media (no device client) | Loses the network-layer auth factor entirely; puts app auth on the public edge |
| Keeping k8s on the identity mesh | Kubernetes is IP-native; bridging it means rebuilding an IP overlay — the design refuted |
| Migrating for its own sake, absent triggers | Recorded decision: no operational driver; elegance is not a driver (mesh-v2 discipline) |

## Open questions (resolve during spike/phase 1)

- ~~Membership cert format: reuse the sovereign-actor delegation-cert
  JSON shape (aligning the two designs) vs a minimal bespoke blob.
  Leaning: the delegation shape, `can: member`, caveats = groups.~~
  Decided 2026-09-03: the delegation shape (decision `5w1` — the
  protocol is this repo's center). What remains is the `can`/verb
  vocabulary — spike `talos-config-359.2`.
- Renewal beat for membership certs (revocation story): 90 days to
  match device re-enrollment cadence, or shorter now that renewal is
  a background dial instead of a human act?
- Desktop presentation: SOCKS/PAC (less code) vs same fake-IP TUN as
  mobile (UX symmetry). Leaning: start SOCKS/PAC, upgrade if friction.
- Gateway placement: one per cluster vs per-node agents also serving
  ingress. Leaning: single gateway pod + node agents only for
  apid/system targets.
- Does the hub's mesh HTTP surface (`/config`, `/hosts`, `/policy`)
  move to an ALPN class or stay HTTPS-only? Leaning: ALPN class
  `mesh/hub/v1`, same handlers.
