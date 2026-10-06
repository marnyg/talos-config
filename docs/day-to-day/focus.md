# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** The app-layer seam. Mesh v3 and the storage tiers are done
(epics at P2, board groomed 2026-10-04); the 2026-10-06 spike round
(`i1il`, `9z4e`, `kanr` — memos in `docs/spikes/`) ruled the next
concrete work: **`a0ys`** — the gateway signs a per-request identity
token, the bridge verifies it for `auth_request` and OIDC, oauth2-proxy
goes. Prereq `5kh`. ADR-0032 (the seam) and ADR-0033 (HTTPS
direction) are Proposed.

**Toward goal:** "Every exposed service authenticates against the
wallet" (`goals.md`) — the wallet-rooted member cert, carried as a
signed token, becomes the login for group-gated apps and the zero-click
path for OIDC apps; SIWE remains the person gate. HTTPS over the mesh
stays deferred (device-local termination when an app forces it).

**Next candidates** (owner to pick):
- `a0ys` after `5kh`; un-defer `95la` (appliance login is a Jellyfin-
  local question now).
- kagent 0.10.3 trial from `docs/spikes/agents-kagent/` (spend-limited
  OpenRouter key; Substrate only on code-exec need + 1.0 GA).
- `bsj` backup target; `dsuj` waits on the Windows data copy.

**Out of scope:** a mesh CA of any shape (ruled out, `9z4e`); Agent
Substrate / k8s 1.37 upgrade (trigger not met); KMS onto 443 (`os8s`);
iroh-ffi read-cancellation (`vh6e`).
