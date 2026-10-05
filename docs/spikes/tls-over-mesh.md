# Spike: HTTPS over the mesh (`*.gw.mesh.internal`)

Bead `talos-config-9z4e` (supersedes `90a`). Items are **verified-in-repo**
unless marked `unverified:` (from memory, no web access; check first).

## Question

Mesh-served apps are reached as `http://<svc>.gw.mesh.internal` inside an
iroh identity stream: the device's daemon dials the gateway member by key,
QUIC encrypts, `cert.Authorize` admits the stream once, and the gateway
terminates the `ingress-http` facet as plaintext HTTP into ingress-nginx
(`config-server/gateway/gateway.go:1-96`; natural ports
`config-server/policy/policy.go:82-85`). Two things are asked:

1. Does anything need browser-trusted HTTPS *inside* that tunnel?
2. If yes, how does the X.509 chain root in the protocol's identities
   (wallet → `speak-as` → member cert) rather than in a parallel CA?

## What plain HTTP costs today

Confidentiality and integrity in transit are **not** at stake: the only
hop outside the device is QUIC, keyed by member keys. The costs are all
on the *browser* side, where `http://` is an untrustworthy origin.

- **oauth2-proxy session cookie is non-`Secure`** — `--cookie-secure=false`,
  `--cookie-samesite=lax`, domain `.gw.mesh.internal`
  (`k8s/apps/oauth2-proxy/deployment.yaml:92-94`). Verified. Concrete
  weakening: the cookie has no `Secure`/`__Host-` protection, so it travels
  on any `http://*.gw.mesh.internal` request. Practical exposure today is
  nil — the only path that can carry that Host is the daemon's tun, which
  is device-local — but it is exactly the ambient-authority-past-the-
  gateway trade-off (invariants.md, structural trade-offs). If
  `docs/spikes/auth-mesh-identity.md` moves login to the gateway's
  `X-Mesh-*` headers the cookie matters less; if it stays cookie-based it
  matters more.
- **OIDC issuer is `http://auth.gw.mesh.internal`** — every relying party
  needs a "plain-HTTP issuer" accommodation: ArgoCD comments
  (`k8s/apps/argocd/config.yaml:27-40`), jellyfin-plugin-sso
  `disableHttps` (`k8s/apps/jellyfin/configmap.yaml:101`), oauth2-proxy
  (`deployment.yaml:82`). Verified. Each new OIDC client is a knob to find;
  some have none (`unverified:` Vaultwarden's vault needs a secure context).
- **Not a secure context** — `unverified:` per the Secure Contexts spec
  only `localhost`/loopback/`file:` are trustworthy without TLS, so on
  `http://*.mesh.internal` browsers hide: Service Workers (PWA install,
  offline cache), Web Push, `crypto.subtle`/`crypto.randomUUID`,
  async Clipboard, WebAuthn, getUserMedia, Web Share. Per app:
  - Jellyfin web (`jellyfin.gw`): `unverified:` registers a service
    worker (silently skipped), Cast sender needs HTTPS; playback (MSE)
    works. The TV never sees this: it uses the raw `jellyfin` splice.
  - seerr: `unverified:` PWA + Web Push notifications need HTTPS; the
    request UI works.
  - SillyTavern: `unverified:` has had `crypto.randomUUID is not a
    function` reports on non-localhost http.
  - ArgoCD, Longhorn, *arr, nzbget, transmission, jackett: UI works;
    "copy" buttons that use `navigator.clipboard` silently no-op.
- **Browser chrome** — "Not secure" in the URL bar on every app.
  `unverified:` Chrome's coming HTTPS-by-default warning exempts
  non-publicly-routable addresses, a list that includes 198.18.0.0/15, so
  the fake-IP choice may dodge the interstitial by accident. No HTTP/2
  (browsers do h2 only over TLS).
- **Android** — the production app is a `VpnService`, not a WebView
  (`android/app/src/main/AndroidManifest.xml`; no WebView under
  `android/app/src`), so it needs `usesCleartextTraffic="true"` only for
  its own hub calls. Browsing happens in Chrome / the Jellyfin app on the
  same device, over the tun. Nothing broken; Chrome shows "Not secure".
- **Mac** (`talos-mesh` daemon, `config-server/meshtun`): same browser
  story; nothing else.

Net: nothing an owner uses daily is broken; weakened are the
secure-context surface, cookie attributes, and one knob per OIDC client.

## Termination options

1. **Gateway terminates TLS** on a new `ingress-https` facet (natural port
   443 beside `ingress-http`), plain HTTP onward to nginx. Keeps ADR-0026
   header injection. Needs a leaf for `*.gw.mesh.internal` on the gateway
   and a root in every client. TLS inside QUIC: double encryption, cheap.
2. **ingress-nginx terminates** — gateway becomes a raw splice to nginx
   :443 (like the `jellyfin` facet). The gateway can no longer inject
   `X-Mesh-*` (PROXY protocol carries addresses, not headers), so
   ADR-0026 and the header-login direction die. Rejected.
3. **Per-app TLS** — N cert lifecycles, same header loss, and each app's
   TLS knobs. Rejected.
4. **Device-local terminator** — the daemon that already fakes DNS and
   IPs also fakes the server: on a flow to `<fakeip>:443` it serves TLS
   with a leaf minted on the spot for that name, and forwards plain HTTP
   over the identity stream exactly as today (`meshtun/tun.go:114`, the
   same flow hook on Android via `config-server/mobile`). Zero change on
   the hub, the gateway, or the protocol; the plaintext hop is kernel-local.
   One small gateway change: `gateway.go:71` `SetXForwarded` would stamp
   `X-Forwarded-Proto: http`, so oauth2-proxy and the apps would redirect
   back to `http://`; the gateway must honour an inbound
   `X-Forwarded-Proto: https` from the stream peer (the peer is the
   authenticated device, and it is the only party that can set it).
5. **None** — status quo.

## Trust-root options

- **A. Parallel CA on the gateway PVC.** Generated once, stored beside the
  gateway's key + Kit. It is identity-adjacent state not re-derivable from
  git (invariant 1) and a second root beside the owner's keys
  (invariant 3; protocol invariant 3 "no root CA"). Losing the PVC means
  re-trusting every client. Rejected.
- **B. Wallet-rooted CA, leaf per gateway.** Two sub-variants, both fail:
  - B1 *CA key derived from the secrets seed* — ADR-0018 is explicit that
    the seed "roots only the age identity, KMS seal keys and recovery
    passphrases … never a signing key". A CA key is a signing key.
  - B2 *CA = the ephemeral hub key under its `speak-as`* — the hub key
    rotates on every redeploy, so the root in every trust store rotates
    with it; browsers cannot follow a `speak-as`. Infeasible.
  Either way B puts X.509 issuance into `protocol/` (today X.509-free) and
  adds a third cert class to the beat. Rejected.
- **C. Per-member leaf from the member's own key.** The member key is
  Ed25519 (`protocol/cert/actorid.go`); `unverified:` browsers do not
  negotiate Ed25519 server certificates (Chrome/Safari/Firefox TLS 1.3
  signature-scheme lists omit it; CA/B Forum forbids it), so the gateway
  would need an ECDSA leaf signed by its Ed25519 key acting as a per-member
  root — one trust-store entry per gateway, and the browser still cannot
  walk the EIP-191/canonical-JSON chain to the wallet. Rejected.
- **D. No TLS; the tunnel suffices.** True for transit. False for the
  browser-side costs above, which are not about transit at all.
- **E. Device-local root (goes with termination 4).** The daemon holds a
  per-device CA, generated on the device, never in transit, name-constrained
  to `.mesh.internal` (`unverified:` Chrome/Firefox/macOS honour
  nameConstraints on locally installed roots). It is a *presentation
  artifact*, not authority: it asserts nothing to anyone but this device's
  browsers, and it is minted only after the daemon has already resolved
  the name from the plane's name map and dialed the member by key. The
  X.509 chain is therefore browser → device CA → (daemon's verification)
  → member cert → `speak-as` → wallet. Compromise radius = one device,
  which is already that device's member-key custody radius.
- **F. Web PKI via a public zone** (e.g. `*.gw.example.net`, DNS-01,
  split-DNS to fake IPs). The only option third-party Android apps and
  TVs trust out of the box, but in-mesh access would then depend on a
  registrar + Let's Encrypt (invariant 3, beyond the stated bootstrap
  exception; invariant 1 "no third-party accounts in any auth path") and
  leak names to CT. Noted as the one honest shortcut; not taken.

## Client trust story

- **Mac (daemon + browser).** E: the daemon mints its CA on first start;
  the nix module (in `~/git/nixos`) adds it to the System keychain
  (`security.pki.certificateFiles`); Chrome/Safari trust it,
  `unverified:` Firefox needs `security.enterprise_roots.enabled` (on by
  default on macOS). Install is already root (tun). B would be the same
  install with a cluster-wide root instead — no cheaper.
- **Android, our app.** No WebView today; it keeps `usesCleartextTraffic`
  for its hub calls. If a WebView ever lands, `network_security_config.xml`
  can trust the app's own CA from `res/raw` — the one Android class where
  E or B needs no user action.
- **Android, third-party apps (Chrome, Jellyfin app).** `unverified:`
  since API 24 apps trust only system roots unless their config opts in;
  Chrome trusts user-installed CAs, most apps (Jellyfin included) do not.
  User install is manual (Settings → Security → Install a certificate).
  So E or B yield HTTPS in Chrome only; `http://` must stay served for
  the rest. Keep both ports live indefinitely.
- **TV.** Raw `jellyfin` splice (`gateway.go:11-12`); no HTTP, no TLS
  question. Android TV user-CA install is `unverified:` absent on most
  firmware; irrelevant while the splice carries the TV.

For every option: `--cookie-secure=true` flips only once *all* browser
clients are on `https://` (a `Secure` cookie cannot be set from an
`http://` response); the OIDC issuer may stay `http://` as an identifier.

## Invariant check

- **1** — A/B1 add non-re-derivable identity state; E adds a device-local
  presentation key (like the member key: born on the device, never
  transits; losing it re-trusts one device, never re-enrolls).
- **2** — E: no server state, verifier unchanged; B: hub issues a third
  cert class per beat; A: PVC state the beat cannot recreate.
- **3** — A/B introduce a root beside the owner's keys (and the protocol's
  "no root CA"); E's root is a key on an owner device; F hands the root to
  Let's Encrypt. B1 breaks ADR-0018's "never a signing key".
- **5** — No option adds a public port; option 4 adds a *fake-IP* port
  443 only, inside the tun.
- **Presentation trade-off** — E keeps the fragility where it already
  lives (fakeip "device-local fiction"); B pushes X.509 into the protocol
  issuer; "expect it to stay the fragile part" argues for E or nothing.

## Recommendation

**Do nothing now.** No deployed app is broken; the costs are cosmetic
("Not secure"), latent (clipboard, PWA, push) or one-knob-per-app (OIDC
issuer). The deferred goals.md line *"HTTPS over the mesh via the
wallet-derived CA"* should be **retired as wrong-shaped**: both
wallet-rooted CA constructions are ruled out above (ADR-0018 "never a
signing key"; a `speak-as` root rotates per redeploy), and a cluster-side
CA buys nothing a device-local one does not, at the cost of a third cert
class. **When an app forces it** (first candidates: a secure-context-only
web UI such as Vaultwarden, or an OIDC client with no plain-HTTP knob),
take **termination 4 + root E**: device-local terminator, name-constrained
per-device CA, both ports served, gateway honours the device's
`X-Forwarded-Proto`. Record that direction now so the goals line stops
steering toward a mesh CA.

## Proposed ADR

ADR warranted (direction decision that retires a stated goal). Status:
**proposed — implement only when an app forces it.** Related: ADR-0018,
ADR-0022, ADR-0026; spike `talos-config-9z4e`.

**Title.** Browser TLS on the mesh is a device-local presentation; no
mesh CA.

**Context.** Services at `*.gw.mesh.internal` are plain HTTP inside an
authenticated, QUIC-encrypted identity stream. Transit is covered; the
browser treats the origin as untrustworthy (no secure context, no
`Secure` cookies, http OIDC issuer). goals.md defers "HTTPS over the mesh
via the wallet-derived CA", a nebula-era phrasing.

**Considered options.** Gateway-terminated TLS with (A) a CA on the
gateway volume, (B) a wallet-rooted CA, (C) member-key leaves; (D) no
TLS; (E) device-local termination with a per-device name-constrained CA;
(F) public web PKI. Details in `docs/spikes/tls-over-mesh.md`.

**Decision.** E, deferred. HTTPS for mesh names is produced by the
device's own daemon, which already produces the name and the IP: a
per-device CA constrained to `.mesh.internal`, a leaf minted per flow
after the name resolves from the plane's map, plain HTTP onward over the
identity stream. The gateway gains no key; the protocol gains no X.509.
`http://` stays served. No mesh-wide CA is ever introduced; B is ruled
out by ADR-0018 (seed never signs) and by root rotation under `speak-as`.

**Consequences.** + No new cert class, no hub/gateway state, compromise
radius one device. + Secure context and `Secure` cookies once all browser
clients are on https. − Trust-store install per device (Mac: nix module;
Android: manual, Chrome only). − Double encryption on the device for
media streams. − One gateway change (honour peer `X-Forwarded-Proto`).
− The lock icon asserts "my daemon verified this", which is the truth but
not what users assume a lock means.

**Confirmation.** `curl --cacert <device-ca> https://argocd.gw.mesh.internal`
succeeds on a trusting device; `rg cookie-secure k8s/apps/oauth2-proxy` is
`true` only after every browser client is on https; `rg -l x509 protocol/` stays empty.

## Open questions

- Which app forces it first? Track candidates in the bead; Vaultwarden is
  the canonical secure-context forcer (`unverified:`).
- Does the Jellyfin Android app opt in to user CAs? (`unverified:`;
  decides whether Android gets https anywhere beyond Chrome.)
- Name-constraint enforcement per browser on locally installed roots
  (`unverified:` all three) — without it the device CA is unconstrained
  and the per-device blast radius claim weakens.
- Should `gateway.go` trust `X-Forwarded-Proto` from *any* admitted
  member or only from device-kind members? (A gateway-kind peer never
  dials ingress-http today.)
- Brief asked for an "Android app WebView" story; the app has none. If
  one is planned, the `res/raw` CA path is the cheapest https client.
