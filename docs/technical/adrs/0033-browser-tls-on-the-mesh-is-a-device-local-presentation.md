# ADR-0033: Browser TLS on the mesh is a device-local presentation; there is no mesh CA

- Status: Proposed — direction decided 2026-10-06, implement only when
  an app forces it
- Date: 2026-10-06
- Related: ADR-0018 (the seed is never a signing key), ADR-0022 (hub
  TLS is web PKI), ADR-0026 (gateway), ADR-0032; spike
  `talos-config-9z4e` (`docs/spikes/tls-over-mesh.md`); retires the
  `goals.md` line "HTTPS over the mesh via the wallet-derived CA"

## Context and Problem Statement

Services at `*.gw.mesh.internal` are plain HTTP inside an
authenticated, QUIC-encrypted identity stream. Transit is covered;
the browser is not told so: no secure context (no Service Workers,
clipboard, WebCrypto), oauth2-proxy runs `--cookie-secure=false`,
every OIDC client carries a "plain-HTTP issuer" knob. Nothing deployed
is broken. `goals.md` has deferred "HTTPS over the mesh via the
wallet-derived CA" since 2026-07-31 — a nebula-era phrasing that
assumed a cluster-side CA. Where would TLS terminate, and what roots
it?

## Decision Drivers

- Invariant 1: a CA key is identity state unless wallet-rooted and
  re-derivable. ADR-0018: the secrets seed never signs; the hub's
  signing key is ephemeral and rotates per redeploy.
- Invariant 3: roots of trust are owner-held keys, never a key on a
  pod.
- The structural trade-off: the presentation layer (fake IPs,
  `*.mesh.internal`, browser TLS) is where the model meets a web that
  assumes global names and web PKI — keep the fragility there.
- `.internal` is ICANN-reserved: no public CA will ever issue for it.
- Browsers do not accept Ed25519 TLS leaves (`unverified:` per
  browser), so a member key cannot be its own leaf.

## Considered Options

### Option A: CA on the gateway's volume

- Cons: non-re-derivable identity state on a pod; a root beside the
  owner's keys (invariant 3).

### Option B: wallet-rooted CA (seed-derived, or `speak-as` ephemeral key)

- Cons: seed-derived contradicts ADR-0018; the ephemeral form rotates
  the trust-store root on every hub redeploy. **Ruled out both ways.**

### Option C: per-member leaf from the member's own key

- Cons: Ed25519 leaves are not browser-trusted; nginx/per-app
  termination loses ADR-0026's header injection.

### Option D: no TLS

- Pros: nothing to build. Cons: the browser-side costs above persist.

### Option E: device-local termination with a per-device name-constrained CA (chosen)

The daemon that already fakes DNS and IPs for `*.mesh.internal`
terminates TLS too: a per-device CA, generated on the device, never in
transit, `nameConstraints` = `.mesh.internal`, installed once into
the device's trust store; a leaf minted per name after the daemon has
resolved it from the plane's name map and dialed the member by key;
plain HTTP onward over the identity stream with
`X-Forwarded-Proto: https`.

- Pros: no new cert class, no hub/gateway/protocol state; compromise
  radius one device — already that device's member-key custody
  radius; the X.509 chain is browser → device CA → (the daemon's own
  verification) → member cert → `speak-as` → wallet.
- Cons: trust-store install per platform (Mac: nix module; Android:
  user CA, Chrome only — third-party apps targeting API ≥ 24 ignore
  it); double encryption on the device for media streams; the lock
  icon asserts "my daemon verified this", which is true but not what
  users assume a lock means.

### Option F: public web PKI via a public zone

- Cons: in-mesh access depends on public DNS and a CA (invariants
  1/3); the only path third-party Android apps trust natively —
  declined.

## Decision Outcome

Chosen: **Option E, deferred until an app forces it** (a
secure-context-only web UI, or an OIDC client with no plain-HTTP
knob). Until then plain HTTP is served and no mesh-wide CA is ever
introduced. The `goals.md` deferred scope is reworded accordingly.

### Consequences

- The gateway gains no key; `protocol/` gains no X.509.
- One gateway change when built: honour the peer's inbound
  `X-Forwarded-Proto` instead of stamping `http`
  (`config-server/gateway/gateway.go:71`).
- Secure cookies and `https://` OIDC issuers only once every browser
  client is on https; the TV splice is unaffected.
- Open before building: `nameConstraints` honoured on locally
  installed roots per browser (`unverified:`); which Android clients
  beyond Chrome trust a user CA.

### Confirmation

`curl --cacert <device-ca> https://argocd.gw.mesh.internal` succeeds
on a trusting device; `rg cookie-secure k8s/apps/` is `true` only
after every browser client is on https; `rg -l x509 protocol/` stays
empty. Invalidated if a client class that cannot install a device CA
(a third-party appliance app) ever needs https — then F is the only
path and this ADR is revisited.
