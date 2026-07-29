# Operational Notes

<!-- "Weather, not climate." Current-state quirks an agent should know about but that
     don't belong in AGENTS.md (too verbose / temporal) or technical/ (not landed knowledge).

     Each entry: `YYYY-MM-DD — <note>`.
     docs-update prunes stale items (>30 days old gets `<!-- stale? -->` flag for review). -->

- 2026-07-29 — ~~`nix run .#apply` is dangerous on provisioned machines~~
  Fixed later same day (#30): apply now fetches the hub-composed config
  over the tunnel and refuses to compose locally.
- 2026-07-29 — Every fly deploy re-seals the hub: derived roles (wg,
  KMS, enrollment) are down until a wallet unseal at `/status`. The
  tunnel HTTP listener (hello + admin `/config`) only exists post-unseal.
- 2026-07-29 — `nix run .#apply` now requires being on the wg tunnel as
  an admin peer (`wgup`) and an unsealed hub. `APPLY_HUB` overrides the
  default `http://10.99.0.1`.
- 2026-07-29 — `wgping` binaries built before the tunnel-HTTP change
  hang against the new hub (the hello no longer speaks unprompted —
  rebuild wgping).
- 2026-07-29 — nebula cert-version skew: `nebula-cert` ≥1.10 emits V2
  certs by default; nebula ≤1.9 (e.g. alpine's apk package) can't parse
  them. Anything minting or consuming mesh certs must be ≥1.10; the hub
  embeds 1.11.0.
