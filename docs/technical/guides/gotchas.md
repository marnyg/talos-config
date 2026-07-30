# Gotchas

Traps that have each cost real time at least once. Landed knowledge —
these are properties of the tools, not current weather (that lives in
`day-to-day/notes.md`).

## Nix / Go

- **`vendorHash` and untracked files.** Flakes only see git-tracked
  files, so a new package directory that is not yet `git add`ed is
  invisible to `go mod vendor` — its imports get silently left out of
  the vendor dir, and the build fails with `import lookup disabled by
  -mod=vendor` for a module `go.mod` clearly requires. **`git add`
  before recomputing the hash.** (Bit twice: `filippo.io/age`, then
  `nebderive`/nebula.)
- **The vendor derivation is fixed-output.** Nix reuses *any* store path
  matching the hash, so a stale-but-matching vendor dir survives a
  `go mod tidy` and keeps being reused. Symptoms are a missing package
  or "inconsistent vendoring" — *not* a hash mismatch. Force a recompute
  by setting a bogus hash and reading nix's `got:` line.
- **`go vet` does not treat `log.Fatal` as terminating.** Code after it
  is unreachable but unflagged, so a misplaced brace can orphan a whole
  block into dead code that compiles and passes every unit test. Found
  this way: the `WG_MASTER_KEY` auto-unseal, orphaned by a mis-scoped
  `else if`. Run the binary, not only the tests.
- **Never trust a hand-rolled encoding without an oracle.** A bech32
  generator constant typed from memory had one bit wrong
  (`0x26508e6b` vs `…6d`); only a round-trip test against the real age
  parser caught it.

## Networking

- **Hub netstack MTU is 1280**, so every WG client must clamp to ≤1240
  or TLS blackholes mid-handshake while pings still pass. `wgquick`
  output emits `MTU = 1240` for this reason.
- **kube-proxy in nftables mode** binds NodePorts to the primary node IP
  only. Fixed with `proxy.extraArgs.nodeport-addresses` plus a live
  DaemonSet patch — Talos renders bootstrap manifests once, so config
  changes do not reconcile them on a live cluster.
- **Nebula cert versions.** `nebula-cert` ≥1.10 emits V2 certs by
  default and nebula ≤1.9 cannot parse them. Anything minting or
  consuming mesh certs must be ≥1.10. See ADR-0002.

## Kubernetes / Talos

- **Talos host DNS does not serve `/etc/hosts` to pods.**
  `extraHostEntries` + `hostDNS.forwardKubeDNSToHost` looks like it
  should give every pod the host's static names — it deliberately does
  not (siderolabs/talos#9822, #13141): kube-dns forwarding bypasses the
  hosts file. Pods that must resolve a mesh name (OIDC issuer fetches)
  need per-pod `hostAliases`. Also: Talos re-applies its bootstrap
  manifests, so hand-edits to the CoreDNS ConfigMap revert on reboot.

## OAuth / OIDC

- **`golang.org/x/oauth2` sends `client_id` as HTTP Basic auth first**
  (empty password, RFC 6749 §2.3.1) and only retries with form params
  after a failure. A token endpoint that reads only the form burns a
  single-use code on the Basic attempt and the retry dies on the dead
  code — every exchange fails with `invalid_grant` while logs show two
  different errors. Cost the first real ArgoCD sign-in; fixed in
  `siweoidc` by accepting both styles (`75fb4e7`).

## Supply chain

- **Any helm-repo Application is a rebuild time bomb.** The Bitnami
  chart purge 404'd `bitnami-labs.github.io/sealed-secrets` and silently
  killed the controller. Vendor release manifests instead.

## Operational

- **`fly logs` replays old buffered history** — filter by timestamp or
  you will debug yesterday.
