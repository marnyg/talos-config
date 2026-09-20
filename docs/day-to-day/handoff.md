# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-20 (twelfth session) — **the nebula scaffolding that P2.4
held up is demolished: `vftt`, `ri3b`, `xnat` all landed and are
deployed.** P2.4 (`359.9.4`) closed.

- **`f4942eb` (`vftt`)**: `jellyfin.cp1` cut — the Ingress host, the
  siwe-oidc redirect origin, the comments that kept it. Nothing is
  named `*.cp1.mesh.internal` in k8s any more.
- **`06b3cfd` (`ri3b`, −1660 LOC)**: the nebula listener's `GET
  /hosts` + `GET /policy`, the HTTPS `/policy` overlay page,
  `SetPolicyOverlay`/`composeEffective`, `policyclient/` and nebup's
  policy-sync loop deleted. `effectivePolicy` reads git only.
  ADR-0014 carries a revision note; the overlay half is gone from
  code. The overlay listener serves the hello alone and the e2e test
  pins that the cut routes 404.
- **`fdefba6` (`xnat`)**: ingress-nginx is a Deployment + ClusterIP
  Service, namespace back to PSS **baseline**, the `1gv` geo/map gate
  and `mesh-identity-headers` ConfigMap gone, gateway dials the
  Service by name. **Verified live**: `:80` on `10.0.0.68`/`.71` no
  longer answers; jellyfin → `/web/`, jackett → SSO, argocd 200 via
  `*.gw.mesh.internal`. ADR-0026 revision note.
- **Cluster fault found under the deploy, fixed by hand**: w1's USB
  NIC re-enumerated at the last reboot (`enp0s13f0u1u4` → `enp0s13f0u1`,
  `10.0.0.67` → `10.0.0.71`); flannel on w1 kept polling for the old
  name, so VXLAN cross-node pod traffic was dead (host↔host fine).
  Longhorn engines could not reach the w1 replica, which is what
  pinned the gateway (and the media pods) in ContainerCreating.
  `kubectl -n kube-system delete pod <w1 flannel>` re-picked the
  interface; everything recovered. **This predates the session** —
  it had been latent for ~4.5 h.

## Loose threads

- **w1 has two LAN identities in the wild**: the TV's verified
  LAN-direct path was `*ip:10.0.0.67` (last session); the node's
  address is now `10.0.0.71`. Whether the USB NIC comes back as `.67`
  on the next boot, and whether flannel survives a rename again, is
  unknown. Not filed.
- **TV still holds a Jellyfin admin session** (Quick Connect via
  break-glass); non-admin user not created.
- The last handoff's "Mac cannot reach `jellyfin.gw:8096`" is not a
  thread: `notes.md` (read first) explains it — the `jellyfin` facet is
  granted to `media` only, the laptop is `admins`.
- **Mac daemon still on the pre-`d4960c1` binary**; `~/git/nixos`
  lock bump uncommitted; `darwin-rebuild switch` not run.
- `talos/mesh-policy.yaml` still carries the dead `port 80 / group
  media` and `host: hub` rules — kept on purpose so the nebula render
  stays byte-stable until Phase 4 deletes the file.
- `bh74` (fake-range address advertised) still reproduces on the TV.

## Suggested next steps

XX
  word).
- Re-run `darwin-rebuild switch`, commit the nixos lock bump.
- **P2.5 (`359.9.5`)**: k8s/Talos endpoint off the mesh — the only
  remaining Phase 2 step, and the one that touches cluster
  availability. Read `docs/mesh-v3-iroh.md` P2.5 first.
