# forgejo-runner bootstrap — one-time steps after the first ArgoCD sync

The manifests are declarative; what follows is the state Forgejo keeps
in its database (the runner row, the mirror repo) and the two sealed
secrets. Run once, by hand, from the laptop (on the mesh, with cluster
access). Forgejo's own bootstrap (`k8s/apps/forgejo/bootstrap.md`)
must have happened — steps 1 and 3 there (the `sysadmin` account, the
`mirrors` org).

```sh
F="kubectl -n forgejo exec deploy/forgejo -- forgejo"
```

## 1. The runner secret, sealed and registered

A pre-shared secret instead of a registration token: the runner side
is then a sealed secret the pod re-registers from on every start
(image.nix), and Forgejo sees one runner — its UUID is the secret's
first 16 hex characters. Register it with the same value. Scope is
global (no `--scope`): one runner for every repo the instance hosts.

```sh
S=$(head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n')   # 40 hex chars
kubectl create secret generic forgejo-runner -n forgejo-runner \
  --from-literal=RUNNER_SECRET="$S" --dry-run=client -o yaml \
  | kubeseal --cert talos/clusters/homelab/sealed-secrets.crt -o yaml \
  > k8s/apps/forgejo-runner/runner-secret.sealed.yaml
$F forgejo-cli actions register --name cluster-nix --labels nix:host --secret "$S"
unset S
```

Rotation = both lines again with a new value (the old row in Forgejo
stays until deleted under Site administration → Actions → Runners).

## 2. The Attic token, sealed

Pods push at `attic.nix-cache.svc` with a token of their own
(`k8s/apps/nix-cache/bootstrap.md` "Tokens"). Narrow: pull and push
on `main`, nothing about caches.

```sh
A="kubectl -n nix-cache exec deploy/attic -c atticd -- /bin/atticadm -f /config/server.toml"
$A make-token --sub ci --validity 2y --pull main --push main \
  | tr -d '\n' | kubectl create secret generic attic-ci -n forgejo-runner --dry-run=client -o yaml \
      --from-file=ATTIC_TOKEN=/dev/stdin \
  | kubeseal --cert talos/clusters/homelab/sealed-secrets.crt -o yaml \
  > k8s/apps/forgejo-runner/attic-ci.sealed.yaml
```

Commit both sealed files (signed). The pod starts once the sealed-
secrets controller has unsealed them (ArgoCD polls ~3 min).

## 3. The mirror that carries the workflow

`.forgejo/workflows/nix-cache.yml` lives in this repo on GitHub
(decision 4la8: GitHub is the source of truth); Forgejo runs it from
the pull-mirror `mirrors/talos-config`. A mirror sync is a push event,
so the workflow fires within `mirror_interval` of a push to main (the
initial import fires nothing — `POST …/mirror-sync` or "Run workflow"
for a first run). `private: false`, but the repo is only as visible as
its org, and `mirrors` is not public (forgejo/bootstrap.md step 3), so
anonymous reads 404; the workflow fetches with the run's own token and
does not care.

```sh
read -rs ADMIN_PW   # kubectl -n forgejo get secret forgejo-sysadmin -o jsonpath='{.data.password}' | base64 -d
curl -fsS -u "sysadmin:$ADMIN_PW" -H 'Content-Type: application/json' \
  http://git.gw.mesh.internal/api/v1/repos/migrate -d '{
    "clone_addr": "https://github.com/marnyg/talos-config.git",
    "repo_owner": "mirrors", "repo_name": "talos-config",
    "mirror": true, "mirror_interval": "10m", "private": false,
    "service": "github", "description": "pull-mirror of github.com/marnyg/talos-config"
  }'
unset ADMIN_PW
```

Actions are on for new repos by default (`repository.DEFAULT_REPO_UNITS`
includes `repo.actions`); if the Actions tab is missing: repo Settings
→ Units → enable Actions.

## 4. Check

```sh
kubectl -n forgejo-runner logs deploy/forgejo-runner | head      # "runner: cluster-nix → …", then "declared labels"
```

Site administration → Actions → Runners shows `cluster-nix` idle. The
mirror's Actions tab shows a `nix-cache` run after the next sync (or
"Run workflow" on it). First run is cold: iroh-ffi and three NixOS
closures, an hour or two; the next is minutes. Then from any mesh
machine with the cache in `nix.conf` (nix-cache/bootstrap.md §3):

```sh
nix path-info --store http://cache.gw.mesh.internal/main \
  $(nix eval --raw github:marnyg/talos-config#packages.x86_64-linux.config-server-bin.outPath)
```
