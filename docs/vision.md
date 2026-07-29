# Vision — desired state of this repo

_This is the higher-order goal future sessions should steer toward.
`day-to-day/handoff.md` says where we are; this says where we're going
and — equally important — where we've decided not to go._

## North star

**A machine goes from blank metal to cluster member with exactly one human
act: a wallet signature.**

Everything else — config delivery, identity, network reachability, cluster
bootstrap, disk encryption, secret distribution — is automatic, declarative,
and reconstructible from this git repo plus keys the owner physically holds.

## Trust model (settled — do not relitigate casually)

- **Roots of trust are keys the owner holds**, never accounts someone hosts:
  - `~/.ssh/id_ed25519` — decrypts everything in the repo (`.age` files)
  - wallet `0xf568…9406` — admits machines to the cluster (seed phrase = cluster admission credential; treat accordingly)
- **No identity or membership state outside git + owner-held keys.** All
  authentication reduces to offline verification against the wallet
  (EIP-191 recovery, EOA only — smart-contract wallets would reintroduce an
  RPC dependency), and the entire network (peers, certs, addresses) must be
  re-derivable with zero runtime re-enrollment. No third-party identity
  accounts, no chain RPC in any auth path. Self-hosted token/cert issuance
  is permitted only when issuer keys are wallet-derived.
  _(Amended 2026-07-29 — was "No OIDC providers…"; the ban was on
  third-party trust dependencies, not the protocol. See
  `docs/mesh-v2-nebula.md` and ADR-0002.)_
- **Fly.io is trusted infrastructure but not a root of trust**: it holds a
  *dedicated* deploy key (scoped to cluster secrets), never the SSH key,
  and is accepted as a per-boot dependency (recorded decision) with
  passphrase break-glass planned wherever it gates boot.

## Architectural principles

1. **Git is the single source of truth.** The four-layer patch composition
   (`base → cluster → hardware → machine`) stays. Servers derive; they do
   not own state.
2. **Stateless services, derivable state.** Anything the server "remembers"
   must be recomputable from (repo + fly secrets + pure functions). Pattern
   already in use: single-use flow state is in-memory and loss-tolerant;
   WG keys will be HKDF-derived, not stored. If a slice seems to need a
   database, redesign it.
3. **Identity is hardware-anchored** (uuid/mac/serial observed at boot) and
   human-ratified (wallet signature). Approval is per-machine, single-use,
   identity-bound.
4. **Ephemeral facts must not be baked into durable identity.** A DHCP lease
   is not a cluster endpoint. (Learned the hard way — renumbering broke the
   cluster once. Target: DNS-name endpoint; interim: DHCP reservation.)
5. **Secrets plaintext exists only in memory** (tmpfs on fly, never in
   images, registries, or git).

## Desired end state

The config server is a **minimal provisioning plane** — one Go binary that:

- [x] composes and serves machine configs (machinery-native, byte-faithful)
- [x] authenticates machines via OAuth device flow, humans via wallet
- [x] runs on fly with TLS, UDP, and tmpfs-only secrets
- [x] maintains an overlay control channel to every machine so nodes are
      reachable from anywhere, on any LAN (today: hub-and-spoke WireGuard;
      end-state: nebula mesh with direct peer paths, hub as
      lighthouse/relay — see `docs/mesh-v2-nebula.md`)
- [ ] bootstraps fresh control planes itself over the tunnel (zero-touch
      after the wallet signature)
- [ ] serves a status page (SIWE session login): machine liveness, last
      config fetch, Talos state — self-reported reality, not stale metadata
- [ ] acts as the disk-encryption KMS (per-boot auth, revocation of stolen
      hardware, passphrase break-glass slot)
- [ ] eventually: mints **short-lived admin talosconfigs** after wallet auth,
      retiring the long-lived god cert (it already holds the CA; the May-2026
      silent cert expiry showed why this matters)

## Explicit non-goals (the Omni line)

This is a provisioning plane, **not a fleet management plane**. We will not
build: upgrade orchestration, cluster templates, proxied kubectl/talosctl
for daily use, multi-cluster management, or a UI beyond the status page.
**If the homelab ever genuinely needs those, the answer is adopting
self-hosted Omni, not growing this codebase into a bad copy of it.**
(Recorded decision: build-not-Omni was chosen *because* the scope is small;
the choice inverts if the scope stops being small.)

## How future agents should use this

- Check Taskwarrior first (`+repo_5efa11ff`): decisions are `+decision`
  (query `status:any`), direction is in `+sketch` annotations, next actions
  in `+handover`/`+next`.
- Changes to the trust model or the non-goals list deserve an explicit
  conversation with the owner and a new `+decision` — they are the load-
  bearing walls of this design.
- Measure any new feature against the north star: does it reduce the number
  of human acts between blank metal and cluster member, without adding an
  account, a database, or an always-up dependency we haven't consciously
  accepted?
