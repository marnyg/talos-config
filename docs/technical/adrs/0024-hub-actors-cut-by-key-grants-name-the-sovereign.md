# ADR-0024: The hub is several actors cut by key; grants to hub facets name the sovereign; the beat is actor-native

- Status: Proposed _(2026-09-16, grill-design on `talos-config-359.8.2.1`
  — the "define inbox message set + owned state first" task decision
  `vl4` mandated; promote when Phase 1.2 lands against it. Landed so
  far: Issuer `359.8.1`; relay child `359.8.2.2`; Enroll →
  `Issuer#mint-device` and `/.well-known` `359.8.2.3` part 1
  (2026-09-17). Outstanding: `#bundle` (needs `359.8.5`), Provisioner
  as an actor, the hub's iroh endpoint `e8d`.)_
- Date: 2026-09-16
- Refines: ADR-0018 (which named the actors and said "cut by state"
  without saying how many keys), ADR-0015 (where the boot token is
  minted and verified)
- Revises: decision `itb` (hub HTTP surface as a stream facet — the
  beat half of it moves to an actor facet)
- Requires: protocol ADR-0003 (`kau`) — a receiver answers for a
  principal it holds a live `speak-as` from
- Related: `docs/desired-state/domain-model.md` §2 "Hub actors"
  (the table and diagram), `docs/mesh-v3-iroh.md` §Phase 1,
  `protocol/actor` (the runtime these actors run on), `iroh-transport/
  nodeid.go` (`ed:` id ≡ iroh `EndpointId`), beads `359.8.2.1`,
  `359.8.2.3`, `54n`, `mdv`, `kau`, `0bc.3`

## Context and Problem Statement

ADR-0018 made the hub the wallet's hot key: a random per-process
`hubkey` empowered by one unseal-signed `speak-as`, and said the hub is
"several actors cut by the state they must keep — Issuer, Enroll,
Relay, Provisioner — first as a modular monolith where promoting an
actor to its own process is a transport change only". Phase 1.2 has to
build that, and three things were undefined:

1. **How many keys.** In the protocol an actor *is* a keypair. If all
   four share `hubkey`, they are one actor with facets and "promotion
   is a transport change" is false — you would copy a private key
   across processes; and `delegable: false` on the `speak-as` forbids
   re-delegating it to sibling keys.
2. **What a grant to a hub facet names.** `VerifyChain` rule 4 is
   literal — `eff.Target ∋ receiver` — and ADR-0018 rotates `hubkey`
   on every deploy. A member's `#renew` grant naming the old key is
   refused after the deploy, and fetching a fresh grant is itself a
   hub facet behind such a grant: the first beat after every deploy
   deadlocks. `xfx` keeps the *member cert* alive across rotation; it
   does nothing for the grants that reach the hub.
3. **What the beat is.** `#renew` is an envelope facet (protocol
   ADR-0001); `itb` put grants, blocklist and the name map on
   `hub-http`, HTTP over an ALPN-gated stream. Two mechanisms per beat,
   and HTTP bodies are unsigned so the name map and blocklist would
   need their own signed-document format.

## Decision Drivers

- Protocol: an actor is a keypair; authority over an actor originates
  at that actor (its consents root every chain); the transport peer is
  a hint, never authority.
- Invariant 2 (actor-owned state; an ephemeral-key actor holds nothing
  durable) and invariant 3 (the hub is never a root of trust).
- ADR-0018's stated outcome: kill any hub process and lose nothing but
  in-flight delegations — which must include surviving the redeploy
  that rotates `hubkey`.
- One primitive (`5w1`): no second authority mechanism, no second
  signed-document format beside the cert and the envelope.
- Owner UX: one wallet act per unseal; enrollment flows unchanged.

## Considered Options

### Keys

- **A. All hub actors share `hubkey`** — rejected (point 1 above).
- **B. One key per actor; only the Issuer's is `speak-as.aud` (chosen).**
  Enroll and Provisioner hold per-process keys the wallet never sees;
  the Issuer consents to them at boot (`invoke {target: hubkey,
  facet: mint-device | mint-machine}`) and verifies the proof *inside*
  their requests — the wallet's approval signature for devices, the
  git-declared machine for MACs — never their authority. Relay is a
  transport component, not an actor.

### Target of a grant to a hub facet

- **C. `target: hubkey`** — rejected: deadlock after every deploy.
- **D. A bootstrap facet admitting a member cert alone** (or `aud: "*"`
  + postage, the M3 frontdoor) to re-fetch `hubkey`-targeted grants —
  rejected: it is the "any cert I signed authorizes asking" special
  case the glossary already forbids for `#renew`, back through a side
  door.
- **E. Stable `hubkey` from the secrets seed** — rejected by ADR-0018
  (the random per-process key is the point).
- **F. `target: wallet`; the receiver answers for a principal it holds
  a live `speak-as` from (chosen; protocol ADR-0003, `kau`).** Hub
  facets are the *sovereign's* facets — `Owner#renew`, `Owner#bundle` —
  served by whichever hot key holds the unseal. Stable forever.
- **G. Also dial and address the wallet** (`iroh:id=` hint,
  `to.target: wallet`, `reach-me-at` for the wallet under the
  `speak-as`) — rejected: an `ed:` id *is* the iroh `EndpointId`; the
  TLS pin and the Reply `from == to.target` check hang on that, and an
  `eth:` id has no endpoint by design. Only the **grant** names the
  wallet; envelope `to.target` and the dial id stay `hubkey`, which the
  member learns from the `speak-as` it must hold anyway.

### The beat

- **H. `#renew` + HTTP `/policy`,`/hosts` over `hub-http`** (`itb`) —
  rejected: two mechanisms, unsigned bodies, an HTTP-over-stream client
  in every member.
- **I. `#renew` + actor facet `#bundle` (chosen; decision `mdv`).**
  `#bundle {}` → Reply `{grants for the caller's groups (hubkey-signed),
  blocklist, name map, current speak-as}`; the caller's groups come
  from the member cert in its own chain; the Reply signature covers
  everything. `hub-http` shrinks to `/config`.

## Decision Outcome

Chosen: **B + F + I.** Concretely (table and diagram in the domain
model §2 "Hub actors"):

- **Issuer** — key `hubkey` (= `speak-as.aud` = the hub process's iroh
  `EndpointId`); facets `#renew`, `#bundle`, `#mint-device` (from
  Enroll, in-memory transport), `#mint-machine` (from Provisioner,
  in-memory), stream `hub-http` → `/config`. Consents at boot: to the
  wallet (delegable; root of every member chain), to Enroll's key, to
  Provisioner's key. State: `speak-as` in memory; location cache, `seq`
  high-water marks, `lw` as safe-to-lose caches; the git checkout as
  compiler input. Nothing durable.
- **Enroll** — own key, in-memory endpoint only; WAN HTTPS device flow
  and wallet approval unchanged; asks `Issuer#mint-device`. Refuses to
  start a flow while the hub is sealed (no wasted wallet act, no
  queue).
- **Provisioner** — own key, in-memory endpoint only; WAN HTTPS
  `/config` (injects the boot token), `/enroll/machine` (verifies the
  token, asks `Issuer#mint-machine`), KMS; the only holder of the
  secrets seed. **The boot token is Provisioner-local** — minted and
  verified by the same actor, so `54n` (HMAC from seed vs per-process
  key) is its own choice. A compromised Provisioner already hands blank
  machines any config; requesting machine certs adds no new power.
- **Shell** (not an actor): mux, `/unseal` (one EIP-712 signature →
  `speak-as` to the Issuer, seed to the Provisioner), `/sealed`,
  `/status`, `/.well-known/…` serving the current `speak-as`, the relay
  child process and its reverse proxy.
- **Lighthouse in Phase 1 is a view**: every inbound envelope
  piggybacks the sender's `reach-me-at` into the Issuer's location
  cache, and `#bundle`'s `NodeId → {port: facet}` half reads it.
  `#publish`/`#lookup`/`#frontdoor` as generic facets stay in M3
  (`0bc.3`) for actors that are not already talking to you.
- **Cold cache after a deploy**: a member lacks only the new
  `speak-as`. `GET /.well-known/…` over WAN HTTPS serves it —
  wallet-signed, verified offline; web PKI is a hint channel. The mesh
  depending on WAN is invariant 4's permitted direction; the document
  is on invariant 5's single entrypoint.
- **Sealed**: relay up; Issuer replies a signed `sealed` status to
  `#renew`/`#bundle`; Provisioner and `/.well-known` 503; Enroll
  refuses at flow start.

### Consequences

- Protocol work before `359.8.1`: `kau` (rule 4 extension, model first
  in `authorize.qnt`, then Go), beside `xwu` and `7ei`.
- `itb` is revised (`mdv`): `/hosts` and `/policy` do not exist over the
  mesh; `hub-http` keeps `/config`.
- `359.8.2.3` retitled to Enroll + `#bundle` + `/.well-known` +
  shrunken `hub-http`.
- ADR-0015's "new enrollment endpoint (shares the device verify+mint
  core)" is now two facets on the Issuer with two callers; the shared
  core is `Issuer#mint-*`.
- ADR-0018 gains nothing new in the `speak-as` caveats (`cav.verbs`
  stays `{member, invoke}`) — option G, which would have added
  `reach-me-at`, was rejected.
- The domain model's §2 "Policy: payload, not identity" render diagram
  is superseded by `#bundle`; redraw when Phase 1.2 lands (already
  flagged there).
- ~~Open, carried into implementation: the exact `#mint-device`
  payload~~ **Resolved 2026-09-17 (`359.8.2.3`, decisions `0t9`,
  `gci`):** the payload is `{node, name, group, fingerprint, nonce,
  signature}` where `signature` is the wallet's EIP-191 over the **v2
  enrollment message** (`config-server/enrollmsg`: ADR-0012's v1 text
  plus a `node: ed:<hex>` line) — so the wallet, not Enroll, names the
  NodeId, and one signature admits a device to both planes. The Issuer
  rebuilds the message and checks the recovered wallet is the one it
  speaks for; it keeps **no replay state** — Enroll's single-use nonce
  is the replay check, and a replayed approval re-mints the same kit to
  the same node. Sibling consents (Issuer → Enroll) use `target:
  hubkey`: same process, same lifetime; rule F is for member-held
  grants. The Issuer Listens for the process's life and swaps its
  authority set through `actor.Hold` at each unseal.

### Confirmation

Right if: a redeploy rotates `hubkey` and every member's next beat
succeeds with the grants it already holds, after at most one
`/.well-known` fetch; a member's bundle contains no cert whose `target`
is a `hubkey`; killing Enroll or Provisioner mid-flight loses only that
flight; a compromised Enroll key can mint nothing without a wallet
signature it does not have. Wrong if any hub actor needs a sibling's
private key, or any member needs an HTTP client to complete a beat.
