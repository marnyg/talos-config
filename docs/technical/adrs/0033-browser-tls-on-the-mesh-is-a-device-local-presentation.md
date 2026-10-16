# ADR-0033: Browser TLS on the mesh is a device-local presentation; there is no mesh CA

- Status: Accepted — direction decided 2026-10-06; built 2026-10-18
  (`talos-config-eu0m`) when the workouts PWA forced it: a service
  worker and standalone install need a secure context
- Date: 2026-10-06, revised 2026-10-18
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
- Browsers do not accept Ed25519 TLS leaves (TLS 1.3 signature-scheme
  lists omit it), so a member key cannot be its own leaf; leaves are
  P-256.

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
- Built as `config-server/meshtls` (root + leaves + the terminating
  reverse proxy, C-free) wired into `meshtun` for `:443`; both
  daemons (`irohup -tun`, the Android `mobile.Tunnel`) mint the root
  under `<state>/tls`; `irohup`'s privileged setup tries to trust it
  (System keychain / p11-kit) before dropping root. **Found live
  2026-10-16:** under launchd `security add-trusted-cert -d` is
  refused even as root ("authorization was denied since no user
  interaction was possible" — admin trust settings need an
  interactive authorization), so on the Mac the one manual
  `sudo security add-trusted-cert …` the log prints stays; Firefox
  reads the System keychain only at startup, so it needs a restart
  after. The automatic path works for an interactive `sudo irohup`. The gateway honours the peer's
  `X-Forwarded-Proto: https`; ingress-nginx runs
  `use-forwarded-headers` so the apps see it (the controller is
  gateway-only, which is what that knob presumes).
- **`nameConstraints` on a locally trusted root, verified in source
  2026-10-18:** enforced by Safari/trustd (`SecPolicyServer.c` seeds
  the permitted subtrees from a trusted anchor), by Chrome on macOS
  (`trust_store_mac.cc`: `WithEnforceAnchorConstraints`) and by
  Firefox everywhere (mozpkix checks every issuer's constraints,
  anchor included). **Not** by Chrome on Android:
  `trust_store_android.cc` loads user roots with `AddTrustAnchor()`,
  which carries no anchor-constraint enforcement. So on the phone the
  root's reach in Chrome is bounded by the device's custody alone —
  the same radius as its member key, which lives beside it — not by
  the constraint. The root still carries the constraint (critical):
  the daemon never signs outside the zone regardless, and a verifier
  that honours it gets the smaller radius.
- Android apps targeting API ≥ 24 ignore the user CA store unless
  their network security config opts in (Android docs); Chrome opts
  in, the Jellyfin app and the like do not and stay on `http://`.
  Android 11+ lets no app install a CA on its own (only a device
  owner via `DevicePolicyManager.installCaCert`): the app exports the
  root as a file and opens Settings, where the user installs it once;
  on Android ≤ 10 the `KeyChain` install intent does it in one tap.

### Confirmation

`curl --cacert <state>/tls/ca.crt https://argocd.gw.mesh.internal`
succeeds on a trusting device; `https://workouts.gw.mesh.internal`
opens clean in Chrome on the phone and Safari on the laptop, Chrome
offers "Install app" and the service worker registers, `http://`
still answers 200 (confirmed 2026-10-16 — Mac: Firefox lock, 200
both schemes, trustd `verify-cert` passes with the constraint; Sony
XQ-BQ52, Android 13, Chrome: "Connection is secure", `sw.js`
registered on the https origin, `Page.getInstallabilityErrors` empty,
menu offers "Install and create shortcut"; root installed via the
app's Save → Settings flow, PIN/biometric required by Android); `rg cookie-secure k8s/apps/` is `true` only
after every browser client is on https; `rg -l x509 protocol/` stays
empty. Invalidated if a client class that cannot install a device CA
(a third-party appliance app) ever needs https — then F is the only
path and this ADR is revisited.
