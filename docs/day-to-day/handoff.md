# Handoff

<!-- "Where we left off." Overwritten at the end of each meaningful session by docs-update.
     Backward-looking. Resets each session. -->

## Last session

2026-09-21 (thirteenth session) — **P2.5 landed; Mesh v3 Phase 2 is
complete.** `625a2e6` + hub deploy + `nix run .#apply` w1 → cp1, no
reboot, both nodes stayed Ready throughout.

- **Static LAN addresses declared by MAC** in
  `talos/machines/<mac>/patch.yaml` (`deviceSelector.hardwareAddr`,
  `dhcp: false`, default via `10.0.0.1`, resolver `10.0.0.1`): cp1
  `10.0.0.68`, w1 `10.0.0.71` — the addresses the MACs already held.
  **No router DHCP exclusion** (decision `ebis`): adding a node must
  not depend on router access; a pool collision is accepted risk.
- **Cluster endpoint `https://10.0.0.68:6443`** in `cluster.yaml` and
  `worker-cluster.yaml` (was nebula `10.42.218.125`). certSANs now
  state `10.0.0.68`, `cp1.mesh.internal`, `cp1`, and keep
  `10.42.218.125` until Phase 4. The apiserver cert rolled (`DNS:cp1`
  added); kubevirt controllers restarted on leader election — normal.
- **`6gq` resolved by construction**: `talosctl etcd members` shows
  `10.0.0.68:2380/2379`, a declared address.
- **Invariant 4's cluster-membership exception closed** (struck
  through in `invariants.md`): a worker's kubelet reaches the API over
  the LAN; no overlay is needed for membership.
- talosconfig / `nix run .#kubeconfig` untouched — they already went
  via `cp1.mesh.internal` on the tun since P2.1.
- **Fixed en route**: `cmd/irohup/tun_other.go` (linux stub) did not
  compile since `c075081` (`connPool` → `meshtun.Pool`), which broke
  the hub image build (`fly/deploy.sh`). One-liner, `585524e`.
- **Observed**: after a hub redeploy the Mac daemon beat OK with the
  new hub NodeId at 20:13:53, but the tun kept dialing the *old* hub
  NodeId for ~15 s more (in-flight dials). Self-healed; not filed.

## Loose threads

- cp1's `machined` still holds pre-P2.5 ESTABLISHED sockets to
  `10.42.218.125:6443` (loopback-local on nebula0). Gone at the next
  reboot / when nebula stops. Nothing depends on them.
- w1 USB NIC rename → flannel stale-interface failure (notes.md
  2026-09-20) is still live risk on w1's next reboot. The static
  address survives a rename; flannel does not.
- Two kubevirt pods (`virt-controller-…-9q746`,
  `virt-exportproxy-…-xt4kz`) in `Error` since the 09-20 flannel
  outage; the replacement replicas run. Cosmetic; `kubectl delete`
  them when convenient.
- **TV still holds a Jellyfin admin session**; non-admin user not
  created. **Mac daemon still on the pre-`d4960c1` binary**; `~/git/
  nixos` lock bump uncommitted; `darwin-rebuild switch` not run.
- `talos/mesh-policy.yaml` still carries dead nebula rules on purpose
  (byte-stable render until Phase 4). `bh74` still reproduces on the TV.

## Suggested next steps

- **Phase 3 soak (`359.10`)**: nothing to build; wait for the events.
  When w1 next reboots, check flannel's `public-ip` annotation first.
- `darwin-rebuild switch`, commit the nixos lock bump.
- Phase 4 (`359.11`) once the soak's three events have passed.
