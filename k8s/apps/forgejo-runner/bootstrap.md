# forgejo-runner bootstrap — one-time steps after the first ArgoCD sync

The manifests are declarative; what follows is the state Forgejo keeps
in its database (the runner row) and the two sealed secrets. Run
once, by hand, from the laptop (on the mesh, with cluster access).
Forgejo's own bootstrap (`k8s/apps/forgejo/bootstrap.md`) must have
happened — steps 1 and 6 there (the `sysadmin` account, this repo
migrated in as `mar/talos-config`).

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

## 3. The repo that carries the workflow

`.forgejo/workflows/nix-cache.yml` lives in this repo, and this repo
lives on Forgejo as `mar/talos-config` (decision z15k, forgejo/
bootstrap.md section 6), so a push to main fires the workflow
directly. Until 2026-10-20 it ran from a 10-min pull-mirror
`mirrors/talos-config` of GitHub; that mirror was deleted when the
migrated repo took over — running both meant every push built twice.
The workflow reads `$GITHUB_REPOSITORY`, so nothing in it names the
owner.

Actions are on for new repos by default (`repository.DEFAULT_REPO_UNITS`
includes `repo.actions`); if the Actions tab is missing: repo Settings
→ Units → enable Actions.

## 4. Check

```sh
kubectl -n forgejo-runner logs deploy/forgejo-runner | head      # "runner: cluster-nix → …", then "declared labels"
```

Site administration → Actions → Runners shows `cluster-nix` idle.
`mar/talos-config`'s Actions tab shows a `nix-cache` run after the next
push to main (or "Run workflow" on it). First run is cold: iroh-ffi and three NixOS
closures, an hour or two; the next is minutes. Then from any mesh
machine with the cache in `nix.conf` (nix-cache/bootstrap.md §3):

```sh
nix path-info --store http://cache.gw.mesh.internal/main \
  $(nix eval --raw github:marnyg/talos-config#packages.x86_64-linux.config-server-bin.outPath)
```
