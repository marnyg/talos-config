# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-10-12 — **first actor↔actor stream facet (`e6pu.2`, root
ADR-0037): the child streams its pi session to the parent.**

- **`protocol/facethttp`** (moved from config-server, decision `k5hb`):
  stream-facet streams as HTTP connections — `Listener`/`Push`/`Server`
  on the accept side, new `Dialed(rwc, local, peer) net.Conn` on the
  dial side. Imports only `protocol/cert` + stdlib.
- **Chain-on-connect** (`actors/child/task.go`, `actors/cmd/spawn`):
  the child dials the parent's `agent-events` ALPN with a preamble
  `{chain, speak_as}` (each cert `cert.Encode`d); the parent admits
  with `cert.VerifyChain(invoke, …, signer = QUIC peer, facet)` — the
  same rule an inbound envelope gets — and `Observe`s the verified set.
  Not `cert.Authorize`: that is the talos layer (member cert, groups,
  blocklist). Identity past the facet is `{Key: peer}`.
- **Outfit as the carrier**: the parent's `Spec.Outfit` signs the
  one-link invoke chain `{aud: child, target: [parent], facet:
  [agent-events]}` and must install it in its own consents itself
  (`a.EditConsents`) — the Spawner installs only the mandated `#renew`
  cert. `renewHandler`'s `reinstallConsent` keeps it rooted across
  beats; `child.Renew` re-issues it like any parent-issued chain.
- **Handlers run inline in the mailbox loop** — so `#task` acks at
  once and the outcome is the last stream line (`child_settled`).
  No protocol change was needed for any of this (ADR-0005 held).

## Loose threads

- `Spec.Outfit` doc comment should state the consent rule above
  (thread filed in the root scope).
- Whether chain-on-connect deserves its own protocol ADR (vs. a line
  in ADR-0001's framing ruling) — asked of the owner this session.
- Still open from before: `kckm` (talos Provisioner split), `lm2a`
  (lookup-cap in the intro), `fh2y` (cheap rejects still sign).

## Suggested next steps

- Owner's pick stands: M5 money (`0bc.5`), or the parent actor as the
  N=2 consumer (a durable-key actor with a store — root ADR-0037).
