# Invariants

<!-- Properties that must always hold for the system to be in its desired state.
     Short, declarative, falsifiable. If a change would violate one, that change is wrong
     OR the invariant is wrong — never silently both. -->

Distilled from `../vision.md` (trust model + principles) and the
2026-07-29 mesh design session. `vision.md` remains the narrative;
this list is the checkable form.

1. **Stateless identity and membership.** No identity or membership
   state outside git + owner-held keys: all authentication reduces to
   offline verification against the wallet (EIP-191, EOA only), and
   the entire network (peers, certs, addresses) is re-derivable with
   zero runtime re-enrollment. Self-hosted token/cert issuance only
   with wallet-derived issuer keys. No third-party identity accounts,
   no chain RPC in any auth path. _(Amended 2026-07-29, see ADR-0002.)_
   _(Amended 2026-08-05, see ADR-0012.)_ _(Amended 2026-08-16, see
   ADR-0015.)_ **Scope for mesh members (devices and machines):** a
   member's membership is the CA-signed cert it holds; its keypair is
   generated and kept on the member, never derived from the master and
   never in transit. The device *set* is not enumerable from git — it
   is bounded by wallet-signed enrollment acts; the machine *set* is
   enumerable from git (`talos/machines/<mac>/`), and an approved
   machine re-enrolls automatically at boot via a single-use token in
   its served config. Addresses and DNS labels derive from the
   approved name (devices) or MAC (machines), so re-keying or
   re-enrolling never moves a member. Re-derivable with zero runtime
   re-enrollment: hub, CA, addresses, DNS, configs — **not member
   private keys**. _(Amended 2026-09-03, spike `359.2`.)_ **The grant
   is the record:** authority is carried by the party it empowers
   (the grantee stores and presents its certs); grantor-side state
   about issued authority is non-authoritative — a log or projection
   at most — and nothing may depend on it. Renewal re-verifies the
   grantor's own past signature; exact enumeration of current access
   is not a query this system answers.
2. **Git is the single source of truth.** Servers derive; they do not
   own state. Anything a server "remembers" must be recomputable from
   (repo + fly secrets + pure functions). If a slice of *this design*
   seems to need a database, redesign it.
   **Scope: the control plane** — identity, membership, certs,
   addresses, machine config. The **data plane** is excepted: workload
   payload, and the bookkeeping a stateful data-plane service keeps
   about payload it holds (Longhorn's volume/replica/snapshot CRDs).
   That bookkeeping is not recomputable from git and is not required to
   be; it shares the fate of the payload it describes, so its
   durability is a replication-and-backup problem, not a git problem.
   **Git is compiler input, never verifier input** _(2026-09-03,
   spike `359.2`)_: the hub compiles declared roles and policy into
   certs; a verifier (node agent, gateway, any receiver) decides from
   the certs presented to it alone — it never reads git, a registry,
   or a network service to authorize.
   Corollary: **no single node's disk is exempt from a wipe.** A
   reinstall may forget anything that is either re-derivable from git or
   replicated elsewhere. _(Amended 2026-07-31, see ADR-0011; replaces
   the `u-media` carve-out from ADR-0008.)_
3. **Roots of trust are keys the owner holds**, never accounts someone
   hosts (`~/.ssh/id_ed25519`, wallet `0xf568…9406`). Fly.io is
   trusted infrastructure, not a root of trust.
4. **The mesh is post-bootstrap.** Nothing on the provisioning or
   recovery path may depend on the overlay network. Provisioning is
   HTTPS + device flow; recovery works from LAN with owner keys.
   _Scope, clarified 2026-07-31 when the first worker joined:_ this
   constrains **provisioning and admin recovery**, not steady-state
   cluster membership. A worker's kubelet reaches the API server over
   the mesh, because `apiServer.certSANs` carries only mesh names
   (`10.42.218.125`, `cp1.mesh.internal`) — so a worker cannot rejoin
   while the lighthouse is unreachable. Accepted deliberately: the
   lighthouse is a hard dependency of cluster membership. Provisioning
   a replacement node still needs nothing but HTTPS + the wallet.
5. **Single public entrypoint.** The hub is the only public surface
   (HTTPS + its UDP overlay port). No second entrypoint, no home-IP
   pinning into device configs.
   _(The phase-1 dual-overlay exception — wg0 51820 beside nebula 4242
   — closed 2026-07-30 when phase 2 step 3 stripped wg0; the invariant
   holds unqualified again.)_
6. **Machine configuration is hardware-selected and human-ratified.**
   Hardware anchors *configuration*, not key material: the MAC selects
   the node's declared config (`talos/machines/<mac>/`), and approval
   — per-machine, single-use — ratifies the name↔hardware binding.
   Identity keys are minted on the machine, never derived from
   hardware. _(Reworded 2026-08-16, see ADR-0015; was "machine
   identity is hardware-anchored".)_
7. **Ephemeral facts are never baked into durable identity.** A DHCP
   lease is not a cluster endpoint.
8. **Secrets plaintext exists only in memory** (tmpfs on fly; never in
   images, registries, or git).

## Structural trade-offs (consequences of 1–3, stated so they are not "fixed")

_Added 2026-09-05 after the ADR-0015/0017 model review. These are not
defects; they are the price of stateless, owner-rooted, offline-
verifiable authority. A change that removes one of them has almost
certainly reintroduced a registry, an online check, or an authority
above the owner — escalate._

- **Revocation latency ≥ runway, for every cert class.** Revocation is
  expiry and verifiers are offline, so the time a stolen credential
  stays valid equals the time the system survives without the hub
  (`verification/quint/runway.qnt`: 6 d grants, 30 d membership). The
  blocklist is the one negative receiver-side table we tolerate, and
  it propagates on the same beat, so it does not beat the runway
  either (`talos-config-ure`).
- **Stateless verification enforces "may", never "how much".** Any
  counted caveat — rate, spend, single-use, at-most-once — needs
  verifier state, which is volatile here and resets on crash
  (`approval.qnt`, ADR-0015 residual). Economics built on the protocol
  are therefore detection-and-response, not prevention.
- **Capability discipline ends at the actor that terminates the
  stream.** Past the gateway everything is ambient authority (a
  header, a Service, an app trusting its caller); the app layer
  (SIWE→OIDC, ADR-0010) is the seam, and the presentation layer
  (fake IPs, `*.mesh.internal`, browser TLS, OIDC redirects) is where
  the model meets a web that assumes global names and web PKI.
  Expect it to stay the fragile part.
