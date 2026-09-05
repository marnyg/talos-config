# ADR-0018: Unseal is a `speak-as` delegation to an ephemeral hub key; the master shrinks to a secrets seed

- Status: Proposed _(2026-09-06, from spike `talos-config-7vv`;
  promote with ADR-0017 when Mesh v3 Phase 1.1 lands against it)_
- Date: 2026-09-06
- Revises: ADR-0017 (chain depth grows by one link; issuer rules
  operate on the *resolved* issuer); ADR-0012 and ADR-0015 (the
  issuer of member certs is a hot key, not a master-derived CA;
  the boot-token HMAC is keyed from the hub's process key)
- Amends: invariant 2 (state an actor may hold — see
  `desired-state/invariants.md`, 2026-09-06 note); domain-model §2
  ("delegation depth two", "unseal signature IS the key") and the
  glossary entries *Unseal*, *Hub*, *Speak-as*
- Related: `docs/sovereign-actor-protocol.md` §Identity (cold root /
  hot key), `verification/quint/{authorize,runway}.qnt`, spike
  `talos-config-439` (time), `talos-config-fbb` (deploy re-seals)

## Context and Problem Statement

Today the hub's entire derivation tree hangs off one 32-byte master:
`master = HKDF(sig_wallet("talos-config/wg/master/v1"))`
(`config-server/masterderive`). The wallet signature over a frozen
string **is** the secret. From it derive the mesh CA (hence every
member cert), the hub identity, addresses, the age identity that
decrypts the repo's secrets, per-node KMS seal keys and recovery
passphrases.

Three defects follow from "signature as seed", surfaced in the
2026-09-05 model review:

1. **Phishable.** EIP-191 signatures are deterministic (RFC 6979):
   whoever gets the owner to sign that fixed string once — on any
   page — holds the fleet's root, forever.
2. **Unrotatable.** The CA public key is baked into every member's
   trust store; rotating the master means re-enrolling every member.
   The derivation strings are documented as FROZEN for this reason.
3. **The hub is indistinguishable from the owner.** Verifiers trust a
   CA key that only the hub holds; a compromised hub process has
   unbounded, unexpiring, unattenuated authority. The domain model
   drew it honestly: *"Wallet → Master: unseal signature IS the key."*

Meanwhile the protocol sketch already prescribes the fix for
long-lived roots: a **cold root key** that never touches the wire and
**hot keys** carrying short-lived `speak-as` delegations. The hub is
a long-lived root's hot key that we forgot to model as one.

## Decision Drivers

- Invariant 3: roots of trust are keys the owner holds; the hub is
  trusted infrastructure, never a root.
- Invariant 1 / ADR-0017: verifiers decide from presented certs
  alone; git is compiler input, never verifier input.
- One primitive (`5w1`): a second authority mechanism beside the
  delegation cert refutes the protocol before it is built.
- Runway: the hub is sealed after every deploy; whatever the wallet
  signs at unseal bounds how long members keep working without it
  (`runway.qnt`).
- Owner UX: one wallet act per unseal, unchanged enrollment flows.

## Considered Options

### Option A: Keep the master; harden the message (status quo + EIP-712)

Sign a typed-data message instead of a bare string so wallets show
what is being signed.

- Pros: zero architectural change.
- Cons: fixes nothing structural — the signature is still a
  deterministic secret, still unrotatable, still makes the hub the
  owner. Legibility does not bound damage.

### Option B: Replace the master with a `speak-as` delegation to a per-process hub key (chosen)

At boot the hub generates a random Ed25519 keypair. Unseal = the
wallet signs a delegation cert
`{iss: wallet, aud: hubkey, can: speak-as, cav: {verbs: [member,
invoke], groups: […], delegable: false}, exp: +120 d}`.
Member and invoke certs are signed by `hubkey`; the caller's bundle
carries the `speak-as` so the verifier maps `hubkey → wallet` offline.
The master survives only as a **secrets seed** for the material that
is not authority (age identity, KMS seal keys, recovery passphrases).

- Pros: the signature names a key instead of being one — phished, it
  is useless without `hubkey`'s private half, and bounded by `cav`
  and `exp` regardless. Rotation happens on every redeploy for free.
  A compromised hub process is a hot key: bounded verbs, groups and
  lifetime, dies at restart. Verifiers hold no CA — their own consent
  grant is the root and `speak-as` is data. The hub becomes an
  ordinary actor, exactly the protocol's hot-key pattern.
  ADR-0015's boot-token HMAC keyed from `hubkey` makes the
  cross-redeploy replay residual (`vzj`) disappear.
- Cons: one more link per chain (depth three); issuer comparisons
  must resolve through `speak-as`; member runway is now bounded by the
  `speak-as` lifetime too (see Outcome); a stable seed is still
  needed for secrets, so the phishable frozen-message signature does
  not vanish — its blast radius shrinks to "decrypt secrets / unlock
  disks" and becomes rotatable at modest cost.

### Option C: Speak-as with issuance-time validity

As B, but a member cert stays valid after its `speak-as` expires if
the `speak-as` was valid when the member cert was issued (hub asserts
`iat`).

- Pros: decouples member runway from the `speak-as` lifetime.
- Cons: an old `hubkey` whose `speak-as` has expired can keep minting
  by backdating `iat` — the unrotatable root returns in miniature.
  Rejected; chain validity is at **verification time**, as in every
  capability system we borrow from (UCAN, biscuit, macaroons).

### Option D: Durable hub key in a fly secret, wallet signs `speak-as` once

- Pros: no per-unseal wallet act after the first.
- Cons: fly becomes custodian of an *authority* key (invariant 3);
  compromise window is the fly secret's lifetime, not a process's.
  Rejected for authority. Remains an option for the *secrets seed*
  (open question below), where fly would hold secrets but never
  authority.

## Decision Outcome

Chosen: **Option B.** Concretely:

- **Unseal** is the wallet signing one `speak-as` cert to the
  process's fresh `hubkey`. The hub shows the proposed cert (with the
  key fingerprint) on `/unseal`; the owner signs it; nothing about
  the signature is secret. The `MasterMessage` KDF is no longer the
  root of any authority.
- **Speak-as semantics** (canonical text in the glossary): *treat
  anything signed by `aud` as if signed by `iss`, within `cav`, until
  `exp`.* It is a signer→principal map carried by the caller, not
  authority to reach anything. Two axioms:
  1. **Resolve before compare.** Every rule that names an issuer —
     ADR-0017 step 2b, the group-resolution rule — operates on the
     *resolved* issuer. Groups are sovereign-scoped, never hot-key-
     scoped; a member cert from `hubkey_A` and a grant from `hubkey_B`
     resolve to the same wallet and match.
  2. **Verification-time validity.** Effective expiry of a member
     cert is `min(member.exp, speakas.exp)`.
- **Caveats are literal.** `cav.verbs ⊆ {member, invoke}`,
  `cav.groups ⊆` the finite group list in `mesh-policy.yaml`,
  `delegable: false`, `exp`. Names are not caveated: the machine set
  is enumerable but the device set is not (invariant 1), and renewal
  must stay wallet-free (`sqm`). "MACs in git" cannot be a caveat —
  verifiers never read git.
- **Lifetimes.** `speak-as` 120 d; `member` 90 d unchanged;
  `invoke` 7 d unchanged. Because the `speak-as` belongs to the
  *process*, not the beat, member runway is
  `min(member.exp, speakas.exp) − last_beat`: a long-lived process
  can silently approach its `speak-as` expiry. The hub therefore
  **nags** — `/sealed` returns 503 when the `speak-as` has < 30 d
  left, so a wallet act is required at least every ~90 d even with no
  redeploy. `runway.qnt` models the extra bound.
- **The master shrinks to a secrets seed.** It roots only the age
  identity, KMS seal keys and recovery passphrases (the *Provisioner*
  concern), never a signing key. Addresses no longer derive from it
  (Mesh v3: machines have static LAN IPs from git; device IPs are
  device-local). **Open:** whether the seed stays wallet-derived (a
  second signature at unseal, or one EIP-712 act covering both) or
  moves to a fly-held secret — fly as custodian of *secrets* but
  never *authority*. Decided in the actor-owned-state spike derived
  from `7vv`; the default until then is wallet-derived.
- **Hub as several actors, cut by state.** Issuer (mint/renew),
  Enroll (device flow, approval, boot token), Relay (iroh relay, name
  map) and Gateway are pure hot-key actors with no durable secret;
  only Provisioner (config serve, KMS) needs one. First step is a
  modular monolith — one binary, one inbox per actor, messages are the
  protocol's own signed envelopes — so promoting an actor to its own
  process is a transport change only. Detailed in its own spike.

### Consequences

- ADR-0017 amendments: chain depth three; step 2b and the group rule
  read *resolved* issuer; the bundle on connect carries the
  `speak-as`; `authorize.qnt` gains a `speak-as` link and a
  resolution step; `runway.qnt` gains the `speak-as` bound.
- `359.8.1` (`irohderive`) changes shape: no issuer key from
  `masterderive`; `hubkey` is random per process and the unseal page
  signs a cert. Enrollment flows are unchanged for the owner.
- `masterderive` keeps `KMSSealKey`, `RecoveryPassphrase`,
  `AgeIdentity`; loses its role as the root of `nebderive`/issuer
  keys. `MasterMessage` stays frozen for the secrets seed until the
  open question is decided.
- ADR-0015: token HMAC keyed from `hubkey`; `approval.qnt`'s accepted
  cross-redeploy replay residual is removed, and `check.sh` must stop
  asserting it stays.
- Domain-model §2 diagram is redrawn (wallet → speak-as → hubkey →
  certs; master → secrets only); glossary *Unseal*, *Hub*,
  *Speak-as* rewritten.
- `talos-config-fbb` (every deploy re-seals) gains a second trigger:
  `speak-as` nearing expiry.

### Confirmation

Right if: a phished unseal signature, replayed against a fresh hub
process, is rejected (its `aud` is a key that process does not hold);
a redeploy rotates the issuing key with no member re-enrollment and
members converge within one beat; `authorize()` accepts a bundle whose
member and grant were issued under different `hubkey`s; and
`runway.qnt` shows 6 d of starvation still loses no access with the
`speak-as` bound included.
Wrong if any verifier needs a pre-installed hub or CA public key to
accept a chain — that is the master returning as authority, and this
ADR should be superseded.
