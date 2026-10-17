# nix-cache bootstrap — one-time steps and the client side

The manifests are declarative; what follows is the state Attic keeps in
its own database (caches, their keys, tokens) and the configuration on
each machine that uses the caches. Design: spike `talos-config-kyiy`;
the CI feeder: `talos-config-0m50` (`k8s/apps/forgejo-runner`).

## 0. Wait for the pods

```sh
kubectl -n nix-cache get pod,pvc
curl -fsS http://10.0.0.74:5000/nix-cache-info          # mirror, LAN
curl -fsS http://cache.gw.mesh.internal/                 # attic, mesh (any 2xx/404)
```

## 1. The mirror needs nothing

It serves as soon as it is up. Until the OpenWrt router carries the
`cache.lan` record, each LAN machine gets it from `/etc/hosts`:

    10.0.0.74 cache.lan

(nas1's declared address, `talos/machines/6c-bf-b5-05-51-a8/`.)

## 2. Attic: create the cache (no credential)

On the laptop (on the mesh; `attic` is `nixpkgs#attic-client`). The
mesh is the credential: the Ingress's group gate admits the device and
the `gate` sidecar supplies Attic's token (attic-mesh-token.sealed.yaml),
so `attic login` takes no token.

```sh
attic login homelab http://cache.gw.mesh.internal
attic cache create main
# Public: pulls need no Attic identity even past the gate. Priority
# stays attic's default 41 (below cache.nixos.org's 40), so nixpkgs
# paths never cost a round trip here — nix sorts substituters by
# priority.
attic cache configure main --public
attic cache info main                 # → Public Key: main:…
```

### Tokens, when something is not on the mesh path

`atticadm` runs in the pod and signs with the sealed key. Pods (a
builder, CI inside the cluster) reach atticd directly at
`attic.nix-cache.svc:80` and need a token of their own; mint it
narrow and seal it beside the consumer:

```sh
A="kubectl -n nix-cache exec deploy/attic -c atticd -- /bin/atticadm -f /config/server.toml"
$A make-token --sub <who> --validity 90d --pull main --push main \
  | tr -d '\n' | kubectl create secret generic <name> -n <ns> --dry-run=client -o yaml \
      --from-file=ATTIC_TOKEN=/dev/stdin \
  | kubeseal --cert talos/clusters/homelab/sealed-secrets.crt -o yaml
```

The mesh token itself was minted the same way (`--sub mesh-admins
--validity 5y --pull main --push main --create-cache main
--configure-cache main --configure-cache-retention main`). There is no
revocation short of rotating `attic-token-key`, which invalidates all
of them — re-mint the mesh token first when you do.

## 3. Client nix.conf

System-level (`/etc/nix/nix.conf`, or `nix.settings` in nix-darwin /
NixOS), so the substituters are trusted without a `trusted-users`
entry:

```
substituters = http://cache.lan:5000 http://cache.gw.mesh.internal/main https://cache.nixos.org
trusted-public-keys = main:<from cache info> cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
connect-timeout = 5
fallback = true
```

- Off the LAN `cache.lan` is NXDOMAIN and skipped instantly; off the
  mesh the attic name is too. `connect-timeout` bounds the one case
  left — name resolves, host down (nas1 rebooting).
- Acceptance: `nix path-info --store http://cache.lan:5000 $(nix eval --raw nixpkgs#hello.outPath)`
  and the same against `http://cache.gw.mesh.internal/main` with a
  pushed path. (A store *path*, not a flake ref: with `--store` nix
  tries to realise an installable INTO that store, and a read-only
  mirror answers 404 on the upload.)

Pods use the Services instead: `http://nixos-mirror.nix-cache.svc.cluster.local`
and `http://attic.nix-cache.svc.cluster.local/main`.

## 4. Pushing what you build

The cluster feeds itself: the CI runner (`k8s/apps/forgejo-runner`,
`.forgejo/workflows/nix-cache.yml`) builds every x86_64-linux package
of this flake and the NixOS systems of github:marnyg/nixos on each
push to main and nightly, and pushes them here as `ci` (token minted
per "Tokens" above). So an x86_64 machine that follows main rarely
builds anything. Darwin outputs and anything local still need one of
the two ways below, pick per machine:

**post-build-hook** — nix runs it after every local build with
`$OUT_PATHS`; precise (only things built here, never substituted
ones) but synchronous, so a slow push slows the build.

```sh
# /etc/nix/upload-to-cache.sh  (mode 755)
#!/bin/sh
set -eu
export IFS=' '
exec attic push homelab:main $OUT_PATHS
```
```
post-build-hook = /etc/nix/upload-to-cache.sh
```
The hook runs as the daemon's user (root), so root needs its own
`attic login homelab http://cache.gw.mesh.internal` once (no token;
it only records the endpoint), or `HOME` pointed at a config that has.

**attic watch-store** — a user process that pushes every new store
path as it appears: `attic watch-store homelab:main`. Asynchronous,
less precise (pushes substituted paths too, which Attic dedups).

Manual, any time: `attic push homelab:main ./result`.
