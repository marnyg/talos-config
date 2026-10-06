# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** The app seam is built (ADR-0032 Accepted, 2026-10-06): the
gateway signs a per-request identity token, the bridge's `/authz` is
the group gate and `/authorize` logs devices into ArgoCD by it;
oauth2-proxy is gone. What remains is **living with it** — the live
confirmation after sync, then the appliance question `95la` (Jellyfin
login for the TV, which the token cannot reach: splices carry no HTTP).

**Toward goal:** "Every exposed service authenticates against the
wallet" (`goals.md`) — the wallet-rooted member cert, carried as a
signed token, is now the login for group-gated apps and the zero-click
path for ArgoCD; SIWE remains the person gate where per-user state
lives (Jellyfin). HTTPS over the mesh stays deferred (ADR-0033).

**Next candidates** (owner to pick):
- `95la` (per-device Jellyfin accounts vs Quick Connect).
- kagent 0.10.3 trial (spend-limited OpenRouter key; Substrate only on
  code-exec need + 1.0 GA).
- `bsj` backup target; `dsuj` waits on the Windows data copy.

**Out of scope:** a mesh CA of any shape (`9z4e`); a person fallback on
the group gate (ADR-0032 ruling 1); mapping device → person in the
bridge (ruling 2); Agent Substrate / k8s 1.37; KMS onto 443 (`os8s`).
