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
   default relays — packet-capture check). **PASSED 2026-09-13** —
   see §P0.1 below (plain-HTTP relay behind fly TLS, QAD off).
2. **Android feasibility**: iroh (FFI/gomobile) inside a
   `VpnService` with gvisor netstack fake-IP; Jellyfin app streams a
   4K remux ≥ 80 Mbps sustained through it (kill-criterion parity
   with mesh-v2 #3), acceptable battery over a 2h stream.
   **PASSED 2026-09-16** (feasibility, battery, and LAN-direct
   throughput 97 Mbps avg / 154 peak over 10.6 min) — see §P0.2 below.
3. **Talos extension proof**: minimal agent as a system extension —
   boots, dials the hub relay outbound, forwards one inbound
   ALPN-gated stream to apid; survives `talosctl reboot`. **PASSED
   2026-09-15** — see §P0.3 below (imager, not the factory; `/var`
   mounts need `depends: service: cri`).
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

Task `talos-config-ow7`, the P0.4 condition; decision recorded as
**ADR-0021**. Pipeline lives in
`iroh-go/` (own `go.mod`; `protocol/` untouched), flake outputs
`packages.{iroh-go,iroh-go-smoke,iroh-ffi,iroh-relay,uniffi-bindgen-go}`,
`apps.iroh-go-regen`. Bindgen produced a compiling, working Go package
well inside the time-box, so the sidecar proof was **not** built.

| question | data |
|---|---|
| pins landed | iroh-ffi **1.1.0** (git tag; not on crates.io) → its `Cargo.lock` pins core iroh 1.0.2; **patched to core iroh / iroh-relay 1.1.0** (`htt`, 2026-09-06: `cargo update --precise`, regen byte-identical, eval-time assert ties the lock to the pin). uniffi 0.31.2 in the ffi ↔ **uniffi-bindgen-go 0.7.1+v0.31.0** (uniffi 0.31.0) — same minor, contract-version + checksum check passes at `init()`. iroh-relay **1.1.0** from the core repo tag (nixpkgs has 0.95.1). |
| did it generate? compile? | yes / yes after **3 textual fixups** (`iroh-go/regen/fixup.sh`): `HashMap<Vec<u8>,_>` → invalid `map[[]byte]T` (one field); `IrohError.Error()` by value returning `"IrohError"` (vet copylocks); package clause ignores `package_name`. No generator patch needed for async methods/constructors, foreign-implementable trait interfaces (`Preset`, `ProtocolHandler`), error objects, records, enums, `Display/Eq/Hash`. Bindgen also insists on `cargo metadata` in cwd → a dependency-free dummy crate satisfies it offline. |
| smoke: direct | **PASS** — two `Endpoint`s, `PresetMinimal` (no n0 DNS/pkarr/relays), `RelayMode::Disabled`, dial B's bound `127.0.0.1` socket, ALPN `mesh/smoke/v1`, one bi-stream echo, clean close. Selected path `*ip:127.0.0.1:<port>`. |
| smoke: custom relay | **PASS** — `iroh-relay 1.1.0 --dev` on loopback, `RelayMode::Custom(http://127.0.0.1:<port>)`, B dialed by id + relay URL only. Selected path `*relay:http://127.0.0.1:<port>/`, rtt 1 ms. (Probe ran core 1.0.2 client ↔ relay 1.1.0: wire-compatible; since `htt` both are 1.1.0.) Both run in `nix build .#iroh-go-smoke`'s checkPhase and in `go test ./...`. |
| build time | nix cold (aarch64-darwin, M-series): iroh-ffi **7–10 min**, iroh-relay **8 min**, uniffi-bindgen-go **~45 min** (one huge askama crate; built once, cached), bindgen run **1.4 s**, Go build **~2 s**. Warm: Go step only. |
| native lib size | `libiroh_ffi.a` **21.8 MB**, `.dylib` **15.1 MB** (release + LTO, iroh-ffi's profile). |
| Go binary size | smoke **15.0 MB** stripped, Rust archive linked statically; 3.8 MB if dynamically linked to the dylib. |
| CGO required | **yes**, always (uniffi is a C ABI). C toolchain at build time; runtime deps = libc/libSystem (+ macOS frameworks). |
| cross-compile | Go-alone cross-compile is gone: each (os, arch) needs its own `libiroh_ffi.a` + C toolchain → **one builder per target** (`pkgsCross` under nix). **aarch64-linux verified** (nix build in a `nixos/nix` container: both smokes pass, 16.1 MB glibc-dynamic ELF, `NEEDED` = libc/libm/libdl/libpthread/libgcc_s; relay test even hole-punched to direct); **x86_64-linux verified in CI** (`iroh-go.yml`, 2026-09-12: both smokes + bindgen drift green, 18.1 MB glibc-dynamic ELF). Talos static/musl: `pkgsStatic` path identified, **not attempted**. |
| API ergonomics | `preset := iroh.PresetMinimal(); mode := iroh.RelayModeDisabled(); ep, err := iroh.EndpointBind(iroh.EndpointOptions{Preset: &preset, BindAddr: &addr, Alpns: &alpns, RelayMode: &mode})` — then `ep.Connect(iroh.NewEndpointAddr(id, &relayURL, nil), alpn)`, `conn.OpenBi()`, `bi.Send().WriteAll/Finish`, `bi.Recv().ReadToEnd`. Warts: `Option<Arc<T>>` → `**T`; optional scalars are pointers; async calls block the goroutine; objects have `Destroy()`. Usable as-is; a thin idiomatic wrapper is a later nicety, not a need. |
| version bump cost | pins in `nix/sources.nix` → `nix run .#iroh-go-regen` → drift check + smoke → commit lib pins + generated package together. Measured: core 1.0.2→1.1.0 inside the ffi lock = `cargo update` (15 s) + ~4 min host-cargo / **10.5 min cold nix** build, **byte-identical Go output**, smoke green (landed as `htt`). An ffi-surface change adds compiler-guided consumer fixes. Consistent with the P0.4 estimate (4–6 h for the three Go embeddings). |
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

### P0.1 Self-hosted relay on fly — data (2026-09-13)

Bead `talos-config-359.1.1`, branch `spike/mesh-v3-p0`. **PASS on all
four sub-checks.** Scratch app `marnyg-iroh-relay-spike`
(`fly/relay-spike/fly.toml`): upstream image
`n0computer/iroh-relay:v1.1.0`, no build step, config via `[[files]]`.
Probe tool `iroh-go/cmd/p0relay` (`listen` / `dial`, logs the
connection's paths per ping; `nix build .#p0relay-static` = musl binary
for containers/nodes).

| check | result |
|---|---|
| relay behind fly's TLS terminator | **PASS.** The 1.x relay protocol is a WebSocket upgrade on `/relay` (+ `/ping`, `/generate_204` probes), so the relay runs in **plain-HTTP mode** (`tls` absent, `http_bind_addr [::]:3340`) behind `[http_service]`; clients dial `wss://…/relay` through the fly proxy. No cert handling on the relay. Deployed in ~1 min; peers from macOS, NixOS, a Docker container and a fly machine in `ams` all homed on it (rtt 45 ms from Oslo → `arn`). |
| QUIC address discovery (QAD, UDP 7842) | **Deliberately off.** QAD needs the relay to *own* a TLS cert (QUIC can't be proxied by fly's TLS handler) and only serves *remote* hole-punching, which ADR-0006 rules out. Clients still probe 7842 once (3 s timeout at startup, harmless); `iroh-ffi` has `RelayConfig{quic_port: nil}` to silence it — small follow-up in `iroh-transport`. **Trade-off:** a peer behind *any* NAT (even Docker's) has no reachable candidate to advertise; direct paths need at least one side with a LAN-reachable address. That is the production case (nodes, TV, laptop on the LAN). Revisit only if remote-direct ever becomes a goal (DNS-01 cert shipped as a fly secret). |
| two peers, different NATs, via relay | **PASS.** fly machine (`ams`, fly NAT) → NixOS box (home NAT): 10/10 echoes, `*relay:` only, 45 ms. Docker-on-mac (Docker NAT + home NAT) → NixOS: same. |
| co-located LAN-direct hole-punch | **PASS.** Docker-on-mac (behind Docker NAT) → NixOS `10.0.0.11`: first ping over the relay (43 ms), **direct `*ip:10.0.0.11` within 1 s**, 6–17 ms after; NixOS sees `*ip:10.0.0.7:<nat port>`. Two Linux processes on one host: direct within 40 ms. DISCO exchanged LAN candidates over the relay with `udp_v4: false` in the net report — punching does not depend on QAD. |
| n0 infrastructure absent | **PASS** (packet capture inside the Docker peer for a full dial): DNS = `A/AAAA marnyg-iroh-relay-spike.fly.dev` only; destinations = relay `:443` (relay + HTTPS probe), `:80` (captive-portal probe), `:7842` (QAD attempts), the LAN peer, self. Debug tracing (`P0_LOG=debug`) agrees: `PresetMinimal` adds nothing. |

Test-bed findings that cost most of the day and matter beyond the spike:

- **This laptop cannot send LAN UDP from unsigned binaries.** Cisco
  Secure Client *socket filter* + Microsoft Defender network extension
  return `EPIPE` from `sendmsg` for `p0relay` to any `en0` destination
  (own LAN IP included), while `nc`/python/C from the same host to the
  same address succeed and the Tailscale `utun` path works. Relay
  paths are unaffected. Consequence: the Mesh v3 desktop daemon on
  this machine will be **relay-only** unless signed/allow-listed —
  check whether today's nebula gets LAN-direct here either
  (bead filed).
- **Host firewalls block the punch when the other side is NAT'd**: the
  NixOS default firewall dropped the Docker peer's punch until inbound
  UDP on `wlp12s0` was allowed (temporary `iptables -I nixos-fw`). Talos
  nodes have no host firewall by default; the node agent must bind a
  UDP port that stays reachable if a Talos ingress firewall is ever
  enabled. Between two LAN-reachable Linux hosts the punch is symmetric
  and conntrack lets it through either way.
- Two in-process endpoints (`smoke`) hole-punch to `127.0.0.1` even on
  the filtered laptop — an in-process test proves nothing about the
  host's UDP path.

**Embedding shape for Phase 1 (not built here):** the relay is a Rust
binary; `iroh-ffi` binds only the client `Endpoint`, so "in the hub
process" means **config-server spawns `iroh-relay` as a child and
reverse-proxies three paths** (`/relay` WebSocket upgrade, `/ping`,
`/generate_204`) on its existing `:8080` → 443 stays the single
entrypoint (invariant 5). A fly process-group "sidecar" is a separate
machine that cannot share `[http_service]`; a second TLS port (KMS-8443
style) is the fallback. Relay `access` supports an HTTP-POST hook with
`X-Iroh-Endpoint-Id` — the membership gate for later.

The scratch app stays up (shared-cpu-1x, ~$2/mo) because P0.3
(`359.1.3`) dials it.

### P0.3 Talos extension proof — data (2026-09-15)

Bead `talos-config-359.1.3`, branch `spike/mesh-v3-p0.3`. **PASS on all
acceptance checks**, on cp1 (the live control plane, no scratch node).
Agent `iroh-go/cmd/p0agent` (`serve` on the node, `bridge` on the
desktop — `irohup`'s ancestor); extension in `talos/extensions/p0agent/`
(`manifest.yaml`, service spec, `Dockerfile`, `build.sh` = the whole
chain). Static musl binary from `.#p0relay-static` on the NixOS builder,
18 MB; `FROM scratch` rootfs — iroh's relay client carries webpki roots,
so no CA bundle, only a ro bind of `/etc/resolv.conf`.

| check | result |
|---|---|
| boots as a system extension | `ext-p0agent` up at **uptime ≈ 11 s** every boot; with `depends: time: true` (a first-class Talos dependency — the ADR-0019 NTP gate is one line) and `service: cri` it starts ~2 s after cri. Key minted on first boot, `/var/lib/p0agent/key` (0600), loaded on every boot since. |
| dials the relay outbound | online in **3.1 s** after bind, every boot; no inbound port, no config beyond relay URL. Survived a relay-side drop (`Stream closed by server`) and accepted a new connection afterwards without restart. |
| forwards one ALPN-gated stream to apid | `mesh/apid/v1 → 127.0.0.1:50000` (apid; `:50001` in the bead is trustd). `talosctl version/get/logs/upgrade/reboot` all ran end to end through `bridge → iroh → agent → apid`, mTLS intact (dial the node hostname `talos-wu6-eib`, which is in the apid cert SANs, via a hosts entry — `127.0.0.1` is not a SAN). First bytes over the relay, **LAN-direct (`*ip:10.0.0.x`) within one stream** from the laptop's wired `en7` — the Cisco filter (P0.1) bites Wi-Fi `en0` only. |
| survives `talosctl reboot`, reconnects unaided | reboot sequence 40 s, node back 33 s later, agent online at uptime 11 s, **same NodeId**; the bridge got the agent's `CONNECTION_CLOSE` (SIGTERM handler) and redialed in **40 ms**. The node's DHCP lease changed five times during the day (`.42 → .54 → .55 → .56 → .57 → .58`); nothing on the identity path noticed — the Mesh v3 thesis in miniature. |
| installed with `talosctl upgrade`, no wipe | three upgrades (0.0.1 → 0.0.2 → 0.0.3), EPHEMERAL intact each time (key, etcd, Longhorn). |

Findings that shape Phase 1:

- **The Image Factory takes official extensions only.** A third-party
  extension means `imager` (`--system-extension-image …`) and an
  installer image in our own registry (`ghcr.io/marnyg/talos-installer`,
  public; the node pulls unauthenticated). Content-addressed schematic
  ids give way to a tag + digest we own. **`--base-installer-image
  <factory installer>` does NOT inherit the factory's extensions** — the
  first build shipped p0agent alone and cp1 ran ~25 min without
  nebula/iscsi/util-linux (Longhorn CSI crash-looped, recovered on its
  own once iscsi was back). List every extension explicitly
  (`build.sh` does; refs from
  `factory.talos.dev/version/<talos>/extensions/official`).
- **An extension that mounts anything under `/var` must declare
  `depends: - service: cri`.** Upgrade and reboot stop `cri`/`trustd`
  plus their *reverse dependencies* only
  (`v1alpha1_sequencer_tasks.go` `StopServicesEphemeral`), then close
  the LUKS EPHEMERAL volume; a still-running extension pins `/var`
  through its mount namespace and the sequence hangs at
  `teardownLifecycle` ("mapped device is still in use") until the
  service is stopped by hand (`talosctl service ext-p0agent stop`
  unblocked it twice) or the box is power-cycled. The official
  extensions (nebula, iscsid) declare it; the docs do not say why.
- **Drain dominates upgrade time** on the single-node cluster with w1
  down: ~10 min of eviction timeouts per upgrade, install + reboot
  < 1 min. Irrelevant to the design, relevant to how often we iterate
  on the extension.
- `imager`'s installer tarball is tagged with the *base* image's name
  (`ghcr.io/siderolabs/installer-base:<ver>`); retag before pushing and
  drop the local tag or the next build inherits a stale name.

Not exercised (Phase 1): certs and `authorize()` on accept (ALPN gates
the forward table only), `ExtensionServiceConfig` for relay URL/policy
(hard-coded in the spec for the spike), a Talos ingress firewall
(none enabled; the agent's UDP port is ephemeral), staged upgrades.

### P0.2 Android feasibility — data (2026-09-16, PASSED)

Bead `talos-config-359.1.2`, branch `spike/mesh-v3-p0.2`; working plan
and step-by-step log in `docs/mesh-v3-p0.2-android.md`. Owner's phone
(Sony XQ-BQ52, Android 13). Everything on the device is **Go in one
gomobile AAR**: `iroh-go/mobile` (package `p0mobile`: iroh endpoint,
gvisor netstack on the tun fd, fake-IP DNS for `*.mesh.internal`) inside
the spike APK `iroh-go/android-p0/` (`VpnService`, split-routed
`198.18.0.0/15` only). Node side: `p0agent serve` as a stand-in on the
NixOS box forwarding `mesh/http/v1` to the box's own Jellyfin, fed a
synthetic 4K H.264 file at **95 Mbps CBR** (`nal-hrd=cbr`; the library
had nothing ≥ 80 Mbps and w1, which holds the cluster's media, is down).

| check | result |
|---|---|
| `libiroh_ffi.a` for `aarch64-linux-android` | **PASS.** nixpkgs cross (`pkgsCross.aarch64-android-prebuilt`, NDK 27) on the same iroh-ffi 1.1.0 source + patched lock as the desktop build: **zero Rust or lock changes**, ~1 h unattended. ring/aws-lc found the NDK sysroot through the cross stdenv. |
| gomobile bind, iroh statically linked | **PASS.** `libgojni.so` 31.5 MB arm64, `NEEDED` = bionic only, 0 undefined `uniffi_iroh_*`. Three one-line fights: prose in a cgo preamble; `#cgo linux` also matches `GOOS=android` (no libpthread on bionic → `linux,!android`); lld prefers the `.so` the nix output ships next to the `.a` (stage the `.a` alone). `tools.go` keeps `x/mobile` in go.mod. |
| netstack + fake IP + DNS in a `VpnService` | **PASS.** Tunnel up in 3.5 s (relay online) + 211 ms (peer connect). `jellyfin.mesh.internal` → fake IP, non-mesh names forwarded to the underlay through a `protect()`ed socket, TCP flows spliced into per-flow iroh `OpenBi` streams. |
| Jellyfin app plays the file through it | **PASS, Direct Play** confirmed server-side (`/Sessions`: `PlayMethod: DirectPlay`, no transcoder). Firefox as a player buffers forever (no MKV demuxer) — use the app. |
| ≥ 80 Mbps sustained, 4K remux | **PASS — LAN-direct, 97.0 Mbps avg over 10.6 min, 154 peak.** Phone on the home Wi-Fi (`10.0.0.6`), box `10.0.0.11`: path went `*ip:` within the first 5 s and stayed there for **127/127** ticker samples, one iroh session, zero redials, zero relay fallbacks; 121/127 samples ≥ 80 (the rest are the ramp-up and buffer-full pauses). Steady-state minutes read 95.0–95.2 Mbps — pinned to the file's 95.1 CBR, so the *player* is the limiter; the 154 Mbps buffer-refill bursts show the tunnel's headroom. Punched through the box's nixos-fw via conntrack, **no inbound firewall rule**. Earlier the same day, from outside the home, every session was `*relay:` at **48.8 avg / 74.6 peak over 32 min** — with QAD off (P0.1) neither side learns a public `ip:port`, so no WAN punch is even attempted; that number is the fly relay's ceiling, not a design property. Measured on the phone, not the Shield (see below). |
| battery over a long stream | **PASS.** 32 min Direct Play 4K, screen on, unplugged: **99 → 91 %**. `dumpsys batterystats` (computed drain 312 mAh of 3644): Jellyfin app 205 (screen 131, video decode 36, Wi-Fi 28.5); **the tunnel process 8.5 mAh — ≈ 3 % of drain, ≈ 0.4 %/h**. Stopped early: the per-app split, not more minutes, is the number that matters, and it is an order of magnitude below anything a parity comparison could resolve. |

Findings that shape Phase 1:

- **One `VpnService` per device.** Starting the mesh app evicts
  Tailscale (or any other VPN). The shipped app has the same
  constraint; the UX must say so rather than silently win.
- **iroh-ffi's Android network monitor needs a JNI context we do not
  give it.** logcat: `ndk-context: android context was not initialized`
  — a thread panic inside iroh-ffi, non-fatal (the tunnel comes up),
  but network-change detection on the phone is presumably dead. Either
  initialise `ndk_context` from Kotlin at load, or drive redials from
  `ConnectivityManager` callbacks ourselves (the spike's tunnel already
  redials per flow).
- **Interface discovery does work** on Android 13 (iroh listed the
  Wi-Fi, cellular and tun addresses). The `LinkProperties →
  AddExternalAddr` plumbing added on the opposite theory stays as
  belt-and-braces. Diagnostic gotcha: the node-side `remote_addr()` of a
  peer holds *validated* addresses, so an empty set on a relay-stuck
  connection means "no candidate reached us", not "peer sent nothing".
- **Android Private DNS probes DoT (`:853`) at the fake resolver**; the
  netstack forwarded it into iroh where it died at the node. Harmless
  (falls back to `:53`) but the resolver IP should accept `:53` only.
- **The relay is a real ceiling when direct fails**: ~50–75 Mbps to
  one phone from fly's edge. Fine as a fallback, not as a path for 4K.
  Reinforces ADR-0006's stance (remote-direct out of scope) as a
  *known cost*, and makes the membership/QAD questions (`0pq`) worth
  their beads if remote 4K ever becomes a goal.
- **Sideloading a debug APK** on this phone works only via `adb
  install` (the Files-app installer fails silently after Play Protect).
  Packaging is pinned to compressed jniLibs + 16 KB LOAD alignment so
  the same APK also loads on 16 KB-page devices.

Not exercised: the Shield as the client (the throughput bar was met on
the phone; the Shield adds only a wired NIC and a different SoC — the
parents'-TV deployment `4te` is where it gets exercised), cp1 as the node side
(step 7: extension `0.0.4` with the Jellyfin forward), UDP flows other
than DNS, IPv6 inside the tunnel, the `SocketProtector` path under
routes wider than `198.18/15`.

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
