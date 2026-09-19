# Handoff — sovereign-actor protocol

<!-- "Where we left off" for the protocol scope. Overwritten per session.
     Deployment (talos/hub/fly) context lives in the root handoff. -->

## Last session

2026-09-19 — **The first receiver outside the hub** (the talos node
agent, `359.8.3`) drove three small runtime additions and one wire
form, all in `f32474a` / `7c740ff`:

- **`Actor.SeqBase func() int64`** — a sender whose counterparties
  outlive its restarts (a node rebooting against a hub that keeps its
  `HWM`) restarted `seq` at 1 and was a replay until the *receiver*
  forgot. Opt-in: first seq to a receiver this process =
  `max(SeqBase(), 1)`; the agent seeds `UnixNano`. Stateless; a
  rolled-back clock only denies (ADR-0019). Default unchanged (tests
  pin 1). `TestSequenceValidation` covers the restart.
- **`Actor.Observe(verified)` / `RestoreLowWater(lw)`** — a stream
  facet's verifier runs `cert.Authorize` outside the inbox; it hands
  `Result.Verified` to the mark and may seed the mark from a persisted
  value (safe-to-lose, invariant 9).
- **`cert.EncodeBundle` / `DecodeBundle`** — the connect-time bundle
  `{member, grants[], speak_as[]}` as strict wire JSON (unknown key
  anywhere ⇒ reject; member verb checked at decode).
- **Stream-facet wire (iroh-transport, not protocol/)**: one extra ALPN
  per facet; the first bi-stream carries the bundle and gets `ok` /
  `refused: <reason>` back; later bi-streams are raw forwards. The
  connection is the invocation, checked once — the 2026-09-12 ruling,
  now with bytes.

## Loose threads

- **`t29` has its third case.** `Hold` (unseal lifecycle), `Multi`
  (two wires), now `SeqBase`/`Observe` (a receiver outside the inbox).
  The pattern "consumer-driven runtime additions to `actor`" is real;
  draft protocol ADR-0005 for the bucket, or decide it needs none.
- The `seq` glossary line says "lost on restart" for the receiver; the
  sender side (must be monotonic across *its* restarts too) is now
  documented on `SeqBase` and proposed for the glossary.
- The stream-facet preamble reply (`ok`/`refused: …`) is a transport
  convention, not envelope-signed: it tells a caller *why* before it
  spends a stream, and the caller is the untrusted party either way.
  Veto ⇒ silent close.
- Carried: ADR-0001's ≈ 1 h `reach-me-at` text; held `speak-as` out of
  `Result.Verified` (ADR-0003); `DefaultMailbox = 64`; renewal-beat
  fraction (the talos agent chose: renew past half-life or on issuer
  rotation, bundle 6-hourly).

## Suggested next steps

- Nothing queued by the consumer's next step (`359.8.4` irohup dials
  with what exists). Watch for the caller side wanting a pooled
  stream-facet `Conn` per (peer, facet) — that belongs in
  iroh-transport, not here.
- M3 `0bc.3` (lighthouse actor, `#publish`/`#lookup`, PoW postage) is
  the next protocol-side build when picked up.
