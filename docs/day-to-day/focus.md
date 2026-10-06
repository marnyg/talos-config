# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** The app seam reaches the appliances (ADR-0034 Accepted,
2026-10-06): a `media` device's Quick Connect is approved from the
gateway-signed identity token by a sidecar in the Jellyfin pod; the
TV signed in as itself with no password and no human. What remains
is the raw `jellyfin` facet's fate (one door or two) and pinning the
sidecar image.

**Toward goal:** "Every exposed service authenticates against the
wallet" (`goals.md`) — browsers by token or SIWE (ADR-0032), appliances
by token-approved Quick Connect (ADR-0034). The device principal is
consistent across all three; the person is only ever the wallet
(until `7ymy`). HTTPS over the mesh stays deferred (ADR-0033).

**Next candidates** (owner to pick):
- Retire the `jellyfin` splice facet once the TV is on the HTTP door.
- kagent 0.10.3 trial (spend-limited OpenRouter key; Substrate only on
  code-exec need + 1.0 GA).
- `bsj` backup target; `dsuj` waits on the Windows data copy.

**Out of scope:** a mesh CA of any shape (`9z4e`); a person fallback on
the group gate (ADR-0032 ruling 1); mapping device → person outside a
cert-carried binding (`7ymy`); Agent Substrate / k8s 1.37; KMS onto
443 (`os8s`).
