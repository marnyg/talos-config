# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-07-29 (later) — **Mesh v2 phase 1: the node side is written but not
yet deployed.** Two commits, `ec2ed8b..4906454`:

- `nebmachine.go` — a machine's nebula config, CA, cert and key are
  injected into its served Talos config as an **ExtensionServiceConfig
  document** (the extension is a service, not an interface, so this is a
  new document rather than a `machine:` merge). The overlay address comes
  from `buildMeshZone`, so the cert and mesh DNS are the same answer by
  construction — the gap that made `cp1.mesh.internal` resolve to
  nothing is closed in code.
- Node topology and firewall: hub as sole lighthouse/relay/static host;
  inbound closed except ICMP, the hub by cert *name*, and the `admins`
  group. Machines cannot reach each other's control surfaces.
- Installer image → factory schematic
  `011ccccdcfa98314d2550cb33b56426be8f45553fce129a1e6124de63e9f1598`
  (v1.12.6 + `siderolabs/nebula` 1.10.3). fly gets `MESH_ENDPOINT` and
  udp/4242.
- Verified by running the binary, not just tests: `/config` returns a
  two-document config with wg0, disk-encryption slots and the mesh doc
  all intact; nebula's own `configTest` accepts the node config; Talos's
  loader parses and validates the extension document.

## Loose threads

- **Nothing is deployed.** `fly deploy` (re-seals the hub → wallet unseal
  at `/status`), then `talosctl upgrade` to the new schematic, then
  `nix run .#apply` so cp1 receives its mesh identity.
- **certSANs deliberately omitted** (phase 2 step 1): until they land,
  `talosctl -e cp1.mesh.internal` fails TLS, so phase-1 measurement has
  to use ICMP/throughput tests rather than talosctl over the mesh.
- **Non-admin device group** (task 36): the node firewall grants `admins`
  unrestricted access, so a shared-space TV must not be enrolled as an
  admin. Needed before `nebup` enrolls phones/TV.
- Machine leaf certs are valid 5 years and are only re-minted when a
  config is re-served — an expiry cliff, not a revocation control
  (thread uuid dc04e3e8).
- ADR-0003 obligation still unmet: a mesh `/config` route must keep the
  derived-admin-IP gate; the `admins` firewall rule is defence in depth.
- Mobile-app DNS push still unverified (kill criterion 4).

## Suggested next steps

- Deploy in that order (fly → unseal → upgrade → apply) and confirm
  `talosctl get extensionserviceconfigs` / `service nebula` on cp1.
- Then `nebup` enrollment for devices, then the ≥1 week dogfood measuring
  direct-vs-relay and throughput (kill criteria 2–4 armed).
- Practices that keep paying: run the binary, and `nix build` before
  committing — `git add` new files first, since flakes only see tracked
  ones.
