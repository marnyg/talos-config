# ADR-0011: A receiver resolves a root key to an identity through a pin it holds

- Status: Proposed _(2026-10-15, task `talos-config-d0o0`; model
  `d0o0.1` in flight)_
- Date: 2026-10-15
- Amends: root ADR-0018 step 2a (issuer resolution gains one hop),
  ADR-0003 (the receiver-held set grows a third member, `Pins`)
- Related: ADR-0010 (rotation; `Pin`, `Admits`), root ADR-0024
  (grants to hub facets name the sovereign — now the identity
  address), `verification/quint/rotation.qnt`,
  `protocol/cert/rotation.go`, `protocol/cert/authorize.go`
  (`Receiver`, `resolve`), issues `d0o0`, `aja2`

## Context and Problem Statement

Today the consent's `aud` and the key that signs are the same string.
A member consents to `kit.SpeakAs.Iss` (`eth:0xf568…`,
`config-server/nodeagent/agent.go:553`); the hub's allowlist is
`-admin-address` (`config-server/hubseal.go:56-63`); every grant and
every `talos/machines/<mac>` role names that same address. After an
ADR-0010 rotation the root **key** is `eth:0xNEW` — what `Verify`
(ecrecover) yields for the new root's `speak-as` — while every
consent, grant and role still names `eth:0xf568`. Nothing links the
two: `resolve` (step 2a) yields `{signer} ∪ {wallets with a live
speak-as naming it}`, so a chain under the rotated root resolves to
`0xNEW`, which no consent is addressed to. ADR-0010 is inert until a
receiver can say "`0xNEW` is the current root of the identity I
consented to" — and only the receiver may say it (inv 2, 3).

## Decision Drivers

- Inv 2 verbatim: authority is receiver-rooted and offline; the consent
  rooting a chain stays the receiver's own, no registry, no RPC.
- Inv 3: the link key→identity is the receiver's own view; no party
  above the identity (and no caller) may move it.
- ADR-0010's point: rotation changes which key speaks for an identity
  *without* re-issuing a consent, grant or role; otherwise it is
  re-enrollment under another name.
- ADR-0003's discipline: what the receiver holds is a type, never a
  slice a caller's bundle could be confused with. Trivial pin ⇒
  identity map: an unrotated fleet sees no verdict change.

## Considered Options

- **Make the identity the new key** (re-consent everything to `0xNEW`)
  — rejected: every consent, grant and role dies on rotation; that is
  the re-enrollment rotation exists to avoid.
- **Carry the pin / the rotations in the caller's bundle** — rejected:
  a stranger would move or kill a pin by mail. Receiver-held only —
  ADR-0003's rule for `speak-as`, for the same reason.
- **Resolve at the consent instead of at the issuer** (rewrite the
  consent's `aud` to the current root on each beat) — rejected:
  consents are the receiver's own signed durable record; rewriting
  them on every rotation is a registry in disguise and breaks "the
  grant is the record" (root ADR-0017).
- **Receiver-held pins extend issuer resolution (chosen).** Step 2a
  gains one hop: each resolved sovereign S also resolves to identity I
  for every held pin whose live view names S as root (`Pin.Admits`).
  Trivial pin = identity map; verdicts unchanged.
- **Also resolve `cav.target` / `to.target` through pins** — not
  needed: targets already name the identity (root ADR-0024) and rule 4
  resolves them through held `speak-as` (ADR-0003). Nothing on the
  target side mentions a key.

## Decision Outcome

`Receiver{ID, Consents, SpeakAs, Pins}`: `Pins []*cert.Pin` is the set
of identities R has committed to — epoch-0 policy plus every rotation
R has verified and held (`Pin.Hold`). `resolve(c, speakAs, now,
groups)` keeps its set — `{c.Iss} ∪ {s.Iss | live speak-as s naming
c.Iss}` — and then, for each sovereign `s` in it and each `p ∈
r.Pins` with `p.Admits(s.ID)`, appends `{ID: p.Address, EffExp:
s.EffExp, Via: s.Via, ViaPin: p}`. One hop, after the speak-as hop,
never iterated: a pin maps a key to an address, not to another key.
The hop applies wherever `resolve` is used — the member-cert issuer
(2a/2b, `memberSovereigns`) and every grant issuer in `chainUnder` /
`VerifyChain` — so the hub's per-connect check and its envelope path
cannot disagree. A dead view (`View().Dead`, equivocation) admits no
key: nothing resolves to I, every chain under I is unverifiable at R.

Consents stay addressed to the identity address (`kit.SpeakAs.Iss` for
members, `-admin-address` for the hub). What does not change:
`Authorize`'s steps, `VerifyChain`'s fold, rule 4 / `answersFor`,
attenuation, the mark (`lw`: a pin's rotations are guardian-signed,
not R-signed, and no more feed `lw` than a held `speak-as`, ADR-0003).
A `rotate-root` cert still never appears in a chain. The pin is
receiver-held only: `Bundle` has no field for it; a "pin" a stranger
mails is an attenuated twin that is ignored.

Consumer wiring (talos, N=1): `#bundle` gains `rotations` — the list
of rotations the hub holds, each a list of certs, empty today; git
holds the durable copy (ADR-0010: the equivocation record is not
safe-to-lose). The hub builds one trivial pin per `-admin-address`
(`{root: w, guardians: [w], k: 1}`); `unsealIssuer` accepts a
`speak-as` whose `iss` is admitted by some held pin — the view's
current root, today `w` itself. A member pins `kit.SpeakAs.Iss`
trivially on Kit, calls `AssembleRotation` + `Pin.Hold` for each entry
on every `#bundle`, and passes its held pins as `Receiver.Pins`
(`actor.HoldPins`, ADR-0005 family: runtime state, opt-in,
`Authority()` unchanged).

Residual: a receiver's pin is as fresh as its last beat. A member
starved of `#bundle` admits the rotated-away root until the chain
reaches it (ADR-0010's starvation bound); it can never be made to
admit a key its own pin does not derive.

Out of scope: declaring guardians (`4mg`; the policy stays trivial),
the re-pin ceremony after a dead view, the on-chain follower.

- Open: **the old root under its own name.** For a legacy EOA the
  identity address *is* the epoch-0 root key, and `{c.Iss}` is always
  in the resolved set. After I: `0xf568 → 0xNEW`, a cert signed by the
  old key resolves to `eth:0xf568`-the-signer, which the consent to
  `eth:0xf568` matches — the pin hop adds, it never removes. ADR-0010
  pin 3 (`invOldRootDiesOnAdvance`) needs a pinned address to be
  reachable *only* through its pin (drop a resolved `s` with `s.ID ==
  p.Address` unless `p.Admits(s.ID)`). Not resolved here; for the
  model (`d0o0.1`).
- Open: a member's pin advances only on what the hub relays in
  `#bundle`: the hub can starve, never forge (k guardian signatures
  verify at the member against its own pin) — inv 3 holds, but the hub
  is a pin *authority* for the fleet in ADR-0010's N=1 sense.

> A receiver resolves a root key to an identity through a pin it
> holds: beside its consents and its `speak-as` set, it holds one pin
> per identity it committed to, and a sovereign a cert resolves to
> also resolves to every pinned identity whose live view names that
> sovereign as root. Consents stay addressed to the identity. A
> rotation changes which key speaks for an identity; it touches no
> consent, grant or role.
