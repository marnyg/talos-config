# Notes — sovereign-actor protocol

<!-- "Weather, not climate" for the protocol scope. Each entry
     `YYYY-MM-DD — <note>`. Deployment weather lives in the root notes. -->

- 2026-09-12 — **ADR-0001 is Proposed, not built.** `cert.Authorize`
  still builds the fixed 2-link `[consent, grant]` chain and matches
  `aud` raw against `Peer`; `Caveats` has no `Endpoints`/`Postage`;
  `Attenuate` is uncalled. The glossary describes the desired M2
  shape. **Change `verification/quint/authorize.qnt` before the Go** —
  the rapid laws pin them 1:1.
- 2026-09-12 — `protocol/` must not import `iroh-go` (doc.go, iroh-go
  README). The iroh `Transport` is its own module (`0bc.2.6`).
