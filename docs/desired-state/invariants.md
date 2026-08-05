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
   _(Amended 2026-08-05, see ADR-0012.)_ **Scope for mesh devices:** a
   device's membership is the CA-signed cert it holds; its keypair is
   generated and kept on-device, never derived from the master. The
   device *set* is not enumerable from git — it is bounded by
   wallet-signed enrollment acts, and any device's membership is
   re-establishable at any time by one signature. Addresses and DNS
   labels still derive from the approved name, so re-keying or
   re-enrolling never moves a device. The control plane remains fully
   re-derivable: hub, CA, machine identities, addresses.
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
6. **Machine identity is hardware-anchored and human-ratified.**
   Approval is per-machine, single-use, identity-bound.
7. **Ephemeral facts are never baked into durable identity.** A DHCP
   lease is not a cluster endpoint.
8. **Secrets plaintext exists only in memory** (tmpfs on fly; never in
   images, registries, or git).
