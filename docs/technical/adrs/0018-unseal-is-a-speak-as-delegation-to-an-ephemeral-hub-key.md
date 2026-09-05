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
- Related: `protocol/docs/sovereign-actor-protocol.md` §Identity (cold root /
  hot key), `verification/quint/{authorize,runway}.qnt`, spike
  `talos-config-439` (time), `talos-config-fbb` (deploy re-seals)
- Amended 2026-09-06 after the models gained the `speak-as` link
  (`czi`/`jp2`): axiom 1 restated as *one consented wallet vouches for
  both ends* (`9l3`); renewal triggers on hub-key rotation (`xfx`);
  the nag stops serving (`q8h`); grant-side group caveat (`zpf`).
  Marked **[amended]** inline.

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
     resolve to the same wallet and match. **[amended 2026-09-06,
     `9l3`]** A signer resolves to a *set* of wallets (itself plus
     every wallet with a valid `speak-as` naming it in the bundle),
     because any wallet can sign a `speak-as` for any key. Rules
     therefore quantify **one wallet W that R holds a live consent
     for** which vouches for the grant's signer *and* for the member's
     signer — never "the resolved issuers are equal" and never set
     overlap, which lets a stranger wallet bridge two sovereigns' hub
     keys (`authorize.qnt` `invGroupMatchRootedInChain`, mutant m14).
  2. **Verification-time validity.** Effective expiry of a member
     cert is `min(member.exp, speakas.exp)`.
- **Caveats are literal.** `cav.verbs ⊆ {member, invoke}`,
  `cav.groups ⊆` the finite group list in `mesh-policy.yaml`,
  `delegable: false`, `exp`. **[amended 2026-09-06, `zpf`]** Literal on
  both sides: a hot-key-signed grant addressed to `group:g` resolves
  only through a `speak-as` whose `cav.groups ∋ g`, as a member cert
  resolves only through one covering its groups. Names are not caveated: the machine set
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
  **[amended 2026-09-06, `q8h`]** The nag **is a seal**: at < 30 d the
  hub also *stops serving beats*, so no cert leaves with less than the
  30 d member runway behind it. A process serves unattended for at
  most `120 d − 30 d = 90 d`; the bound is tight (`NAG ==
  MEMBER_RUNWAY`, zero margin). Ruled over an advisory nag because the
  failure modes differ in kind: sealing at day 90 costs access at day
  96 (invoke runway) while one wallet act on the *same* process still
  renews every cert; serving to day 120 fails silently at the cliff
  where every member cert is unresolvable at once and every device
  must re-enroll. Order after a missed nag: unseal, one beat, then
  deploy — deploying first strands what the dead key signed.
  **[amended 2026-09-06, `xfx`]** Member renewal triggers at ⅔ life
  **or on the first served beat after the issuing hub key changed**
  (equivalently: off *effective* expiry). Without it a cert signed by
  a dead process keeps that process's `speak-as` expiry, and a
  redeploy at `speak-as` day 90+ strands any cert < 60 d old with the
  hub healthy (`runway.qnt` `rotationConvergesTest`).
- **The master shrinks to a secrets seed.** It roots only the age
  identity, KMS seal keys and recovery passphrases (the *Provisioner*
  concern), never a signing key. Addresses no longer derive from it
  (Mesh v3: machines have static LAN IPs from git; device IPs are
  device-local). **Ruled 2026-09-06 (`qrb`, decision `talos-config-fje`): the
  seed stays wallet-derived, and unseal is ONE EIP-712 typed-data
  signature** whose message carries both the `speak-as` fields and a
  frozen seed domain; the hub verifies the `speak-as` from it and
  HKDFs the seed from the signature over the seed sub-message. Fly
  holds no secret. Accepted cost: the seed half is still a
  deterministic, phishable secret — narrow (repo secrets, KMS,
  recovery) and rotatable. A fly-held seed was rejected (fly as
  custodian; invariant 3/8 exceptions).
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
- ADR-0015 boot token: **open trade-off, not decided here.** Keying
  the token HMAC from `hubkey` closes the cross-redeploy replay
  residual (`vzj`) but kills every token served before a redeploy — a
  machine that fetched its config and has not yet redeemed is
  stranded by the next `fly deploy` (`fbb`: deploys are frequent).
  Keying it from the secrets seed keeps today's behaviour and residual.
  Decide in `approval.qnt` (`54n`) by modelling both; `check.sh`'s
  negative assertion flips only if the model moves to `hubkey`.
- Domain-model §2 diagram is redrawn (wallet → speak-as → hubkey →
  certs; master → secrets only); glossary *Unseal*, *Hub*,
  *Speak-as* rewritten.
- `talos-config-fbb` (every deploy re-seals) gains a second trigger:
  `speak-as` nearing expiry.

### Confirmation

Right if: a phished unseal signature, replayed against a fresh hub
process, is rejected (its `aud` is a key that process does not hold);
a redeploy rotates the issuing key with no member re-enrollment and
members converge within one beat (**the renewal trigger, `xfx`**);
`authorize()` accepts a bundle whose member and grant were issued
under different `hubkey`s *from the same wallet* and rejects one
bridged only by a stranger's `speak-as` pair (`9l3`); and `runway.qnt`
shows 6 d of starvation still loses no access with the `speak-as`
bound included — confirmed 2026-09-06 with the nag sealing (`jp2`).
Wrong if any verifier needs a pre-installed hub or CA public key to
accept a chain — that is the master returning as authority, and this
ADR should be superseded.
