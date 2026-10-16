# nix-cache bootstrap — one-time steps and the client side

The manifests are declarative; what follows is the state Attic keeps in
its own database (caches, their keys, tokens) and the configuration on
each machine that uses the caches. Spike `talos-config-kyiy`.

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

## 2. Attic: admin token, cache, public key

`atticadm` runs in the pod and signs with the sealed key. The token
below is the owner's: every permission on every cache, one year.
It prints once; it lives in `~/.config/attic/config.toml` on the
laptop after `attic login` and nowhere else.

```sh
A="kubectl -n nix-cache exec deploy/attic -- /bin/atticadm -f /config/server.toml"
$A make-token --sub owner --validity 1y \
  --pull '*' --push '*' --delete '*' \
  --create-cache '*' --configure-cache '*' --configure-cache-retention '*' --destroy-cache '*'
```

On the laptop (on the mesh; `attic` is `nixpkgs#attic-client`):

```sh
attic login homelab http://cache.gw.mesh.internal <token>
attic cache create main
# Pulls need no token: the mesh + group gate is the reader's identity
# (attic-ingress.yaml); the token gates writes. Priority stays attic's
# default 41 (below cache.nixos.org's 40), so nixpkgs paths never
# cost a round trip here — nix sorts substituters by priority.
attic cache configure main --public
attic cache info main                 # → Public Key: main:…
```

Further tokens (CI, a second machine) are narrower: `--push main
--pull main` and a shorter validity. There is no revocation short of
rotating the sealed key, which invalidates all of them.

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

Nothing propagates on its own. Two ways, pick per machine:

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
The hook runs as the daemon's user (root): `attic login` must have
been run as root too, or `HOME` pointed at a config that has.

**attic watch-store** — a user process that pushes every new store
path as it appears: `attic watch-store homelab:main`. Asynchronous,
less precise (pushes substituted paths too, which Attic dedups).

Manual, any time: `attic push homelab:main ./result`.
