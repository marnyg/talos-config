# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-21 (eighteenth session) — **P4.2 landed: nebula is out of the
code and off the hub (`359.11.2` ready to close).**

- `600d2d4` — deleted `config-server/{mesh,nebderive,nebstack,nebtest,
  devkey}/`, `cmd/nebup`, `nebenroll*.go` (→ `deviceenroll.go`),
  `slackhq/nebula` + ~30 transitive deps, `talos/mesh-policy.yaml`,
  `mesh-blocklist.txt`, `nickel/mesh-policy.ncl`, the fly `udp/4242`
  service, `MESH_ENDPOINT`/`MESH_CA_PIN`, `--mesh-*`, `recover
  -ca-fingerprint`. −7,400 lines. `enrollmsg.V3(name, group, node,
  nonce)` is the one enrollment message (no pubkey line); the hub
  answers with the bare Kit JSON; the hub is gated on `--iroh-relay`;
  unseal no longer pins a CA fingerprint (wrong wallet fails at the
  age decrypt); `mesh.MachineDNSName` → `machines.DNSName`; `meta.yaml`
  `ip:` gone. New guard: a device name equal to a declared machine or
  `hub` is refused (409 at challenge, 403 at mint) — the nebula render
  used to do this, the witnessed name map would not.
- `737ea8c`/`+1` — `irohup -dns-upstream` dropped (its only reason was
  nebula's DNS beside the tun); `fly.toml` records why the dedicated
  IPv4 stays (KMS `:8443`).
- Hub redeployed 22:20Z on the nebula-free image, hubkey `a65c301d…`,
  unsealed 22:20:27Z (both signatures); auto-bootstrap read
  `etcd-running` off cp1 over the identity plane 8 s later.
  `nix run .#apply` to both nodes without reboot: the inert `nebula`
  ExtensionServiceConfig is gone, `p0agent` doc at v3, `ext-p0agent`
  Running.
- Closed `06j0` (status DNS column via `machineSAN`) and `qoak` (PEM
  carve-out) — both fell out of the deletion.

## Loose threads

- **Clients speak v2 until rebuilt** (`bd` task filed, P3): phone/TV
  APK, gateway pod image, Mac daemon. Harmless while every member
  holds a kit; only the *next enrollment* from a stale binary fails.
- **Dedicated IPv4 `213.188.219.215` kept**: fly shared v4s carry
  80/443 only and KMS disk-unseal is `:8443` (invariant 4). Moving KMS
  onto 443 so the IP can go is `talos-config-os8s`.
- Docs still describe the nebula era in places the code no longer
  does: `desired-state/domain-model.md` (Key = X25519, Runner =
  ext-nebula/nebup, `{config, kit}` envelope, `enrollmsg` v1/v2,
  lighthouse/rendezvous, `mesh-policy.yaml` frozen recipe),
  `invariants.md` #5 ("HTTPS + its UDP overlay port"),
  `technical/deployed-state.md` (`MESH_CA_PIN`, `10.42.0.1`
  lighthouse), `docs/mesh-v3-iroh.md` Phase 4 checklist. All P4.4.
- `-n cp1` still fails by name from the tun (`t7b2`, cp1's generated
  hostname `talos-wu6-eib`); `apply` already routes around it.

## Suggested next steps

- Close `359.11.2` (user confirms), then **P4.3** (`359.11.3`): promote
  ADR-0016's supersession of 0002/0005 and ADR-0017 to Accepted;
  revision notes on 0006/0007/0009/0013/0014.
- **P4.4** (`359.11.4`): the doc rewrite listed above — goals (Mesh v2
  entry becomes history, Mesh v3 "reached"), invariant 5 wording,
  domain-model nebula-era sections, deployed-state, fold
  `mesh-v3-iroh.md` into the exploration log.
