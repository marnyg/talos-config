# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-19 (fifth session) — **w1 is on the identity plane** (`qb5q`)
and the hub runs HEAD. Commits `94d0afe` `7f8652f`; hub image
`registry.fly.io/marnyg-talos-config:7f8652f`, unsealed (hubkey
`abcf8087…`).

- **w1 upgraded in place** to the fleet installer
  `v1.12.6-p0agent-0.1.2@sha256:71d4…` — the same image and digest cp1
  runs; `talos/hardware/alienware-x15.yaml` declares it now, and the
  two hardware yamls are meant to move together. `talosctl upgrade`
  via cp1's apid proxy, ~7 min drain, clean boot, EPHEMERAL intact.
- **The agent enrolled at first boot**, not after the apply: the
  config the user applied at 21:14 was already on disk with a live
  boot token (TTL 1 h), so `ext-p0agent` came up, minted
  `ed:40c9d1ca…` and enrolled as member `w1` group `machines` (cert
  to 2026-12-18). `w1.mesh.internal` → fake IP, `-e w1.mesh.internal
  -n w1` dials apid directly. Both nodes `Ready`.
- **Hub redeployed** so the served `install.image` for w1 matches git
  (it had lagged — the fly image bakes `talos/`). This is what
  stranded the Mac daemon mid-session: `ipt7`.

## Loose threads

- **`ipt7` (P1, new): a hub redeploy strands running `irohup -tun`
  daemons** for up to `DefaultBeat` = 6 h — the member only learns the
  new hubkey at beat or restart, so `hub.mesh.internal` dials a dead
  key and the name map goes stale. Hit live this session; a daemon
  restart fixes it. **Unexplained:** the user saw *general* network
  loss, not just mesh names — a scoped `/15` route plus a
  `mesh.internal`-scoped resolver should not do that. Worth pinning
  down what exactly failed (all DNS? one app?) before assuming.
- **Fake IPs are per-process** — re-minted in first-lookup order at
  every daemon restart (w1 held `198.18.1.1` before, the hub after).
  60 s TTL bounds it; never cache one.
- **cp1 and w1 still carry the old hubkey** until their next beat (6 h
  from their last restart) — harmless for apid (the Mac dials them by
  key), visible as a stale `hub ed:8b723ff8…` in their agent logs.
- **`0q0` stays blocked** — and its blocker is **capacity, not w1's
  liveness**. The bulk PVCs are ~450G on w1; cp1 has ~256G free and
  anti-affinity forces the second replica onto the other node, so
  `numberOfReplicas: 2` cannot schedule. Needs new hardware (ADR-0011
  notes Longhorn's minimum is 3 nodes). **Open question below.**
- **Docs disagree on whether `0q0` should ever be done.** ADR-0011
  and `k8s/apps/storage/storageclass.yaml` treat 1 replica for bulk as
  *correct by design* ("the library is disposable, so it does not earn
  a 2x"); `notes.md` calls the same thing a "knowing deviation from
  invariant 2 — wrong implementation, not a relaxed invariant". Both
  cannot hold. Needs a ruling: either invariant 2's corollary grows a
  third category (owner-declared disposable) or ADR-0011 is a standing
  violation and `0q0` is its fix.
- **Route-churn restart path still unobserved** (`7c3`).
- **Control socket not built** (`fgr`); mobile's own `netstack.go` /
  `dns.go` (`phz`).
- **The hub reads its git blocklist at authorize time** for hub-http
  (mirrors `Issuer.blocked` for `#renew`/`#bundle`). Invariant 2's
  "verifier never reads git" is met by nodes (bundle copy); the hub is
  the compiler — flagged for the next model review, not changed.

## Suggested next steps

- **P2.2 (`359.9.2`)**: hub→node dials (`/status`, bootstrap probes)
  onto identity streams; cut the `nebstack` dial path as it lands
  (`d3z3`).
- **`ipt7`**: make the daemon re-learn the hubkey on stream failure
  instead of waiting 6 h — and first establish what the user's
  general network loss actually was.
- Fire `7c3` once deliberately.
