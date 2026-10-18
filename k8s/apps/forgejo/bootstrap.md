# Forgejo bootstrap — one-time steps after the first ArgoCD sync

Everything in the manifests is declarative; what follows is the state
Forgejo keeps in its own database and cannot take from config: the
break-glass admin, the OIDC auth source, the two orgs, the bot user and
its token. Run these once, by hand, from the laptop (on the mesh, with
cluster access). Each step is idempotent enough to re-run after a
failure; none is safe to re-run blindly after success (duplicate auth
sources, rotated tokens).

The CLI runs inside the pod next to the web process; both are uid 1000
on the same sqlite3 file, which is the supported arrangement.

```sh
F="kubectl -n forgejo exec deploy/forgejo -- forgejo"
```

## 0. Wait for the pod

```sh
kubectl -n forgejo rollout status deploy/forgejo
curl -fsS http://git.gw.mesh.internal/api/healthz
```

## 1. Break-glass admin (local account, like ArgoCD's)

`admin` is a reserved username in Forgejo (`CreateUser: name is
reserved`); the account is `sysadmin`.

The generated password prints once; seal it as `sysadmin.sealed.yaml` beside
this file (see that file's header) — the cluster is the vault.
It is the only local login; everything else signs in with the wallet.

```sh
$F admin user create --admin --username sysadmin --email sysadmin@mesh.internal \
  --random-password --must-change-password=false
```

## 2. The SIWE OIDC auth source

The source name is `siwe` because the callback registered in
`k8s/apps/siwe-oidc/deployment.yaml` is
`http://git.gw.mesh.internal/user/oauth2/siwe/callback` — Forgejo's
callback path is `/user/oauth2/<source name>/callback`. Renaming the
source means changing that line.

`--secret`: siwe-oidc issues no client secret. Its clients are public
clients and PKCE is the proof (`-client=<id>=<redirect>` declares the
id and redirect only; `config-server/siweoidc/http.go` ignores
`client_secret` at the token endpoint, and Forgejo's openidConnect
client sends S256 PKCE — `routers/web/auth/oauth.go`,
`generateCodeChallenge`). Forgejo's CLI still wants the flag, so pass a
placeholder; it is transmitted and discarded. Fallback if a future
siwe-oidc starts enforcing secrets: a confidential-client mode there,
not a change here.

```sh
$F admin auth add-oauth --name siwe --provider openidConnect \
  --key forgejo --secret pkce-public-client \
  --auto-discover-url http://auth.gw.mesh.internal/.well-known/openid-configuration \
  --scopes openid,profile,email,groups \
  --group-claim-name groups --admin-group admins \
  --skip-local-2fa
$F admin auth list
```

`--admin-group admins`: the wallet in siwe-oidc's `-admin` map is
`mar:admins`, so the person's auto-registered user is a Forgejo admin
(same mapping ArgoCD makes to `role:admin`).

Check: open http://git.gw.mesh.internal/ in a mesh browser, "Sign in
with siwe", wallet signs, lands on a user named after the member
(`preferred_username`). If it prompts for a username/email instead, the
claim mapping is off — see `FORGEJO__oauth2_client__USERNAME` in
deployment.yaml.

## 3. Orgs `agents` and `mirrors`

Via the API with the admin from step 1 (basic auth is accepted for
API calls by a local account). `mirrors` holds pull-mirrors
(talos-config included — read-only copies; decision 4la8 keeps the
source of truth on GitHub); `agents` holds the repos agents write to.

```sh
read -rs ADMIN_PW   # paste the step-1 password
G="curl -fsS -u sysadmin:$ADMIN_PW -H 'Content-Type: application/json' http://git.gw.mesh.internal/api/v1"
eval $G/orgs -d '{"username":"agents","visibility":"private","repo_admin_change_team_access":false}'
eval $G/orgs -d '{"username":"mirrors","visibility":"public"}'   # public = anyone past the mesh gate; agents stays private
# (If it was created private: PATCH /orgs/mirrors {"visibility":"public"} is a
# no-op on Forgejo 11.0.16 — flip it in the UI: org settings → Visibility.)
```

Pull-mirrors are added per repo in the UI (New migration → mirror) or
with `POST /api/v1/repos/migrate` `{"clone_addr":…,"mirror":true,"repo_owner":"mirrors",…}`;
which repos is the orchestrator's call, not this file's.

## 4. Bot user `agent`, writer in `agents`

A dedicated team rather than Owners: write on code/issues/pulls across
all org repos, may create repos, cannot delete the org or its repos.

```sh
$F admin user create --username agent --email agent@mesh.internal \
  --random-password --must-change-password=false      # password unused; discard
TEAM=$(eval $G/orgs/agents/teams -d '{"name":"bots","permission":"write","includes_all_repositories":true,"can_create_org_repo":true,"units":["repo.code","repo.issues","repo.pulls"]}' | jq .id)
eval $G/teams/$TEAM/members/agent -X PUT
```

## 5. Mint and seal the agent token

The token is the only credential the sandbox pods carry (ADR-0037:
credentials are provisioner policy, the wire carries names). It is
shown once; it goes straight into the SealedSecret and nowhere else.
`FORGEJO_URL` is the in-cluster Service — pods cannot resolve the mesh
name, and the agent's pushes need not round-trip ingress-nginx.

```sh
TOKEN=$($F admin user generate-access-token --username agent \
  --token-name sandbox --scopes write:repository,write:issue --raw)
kubectl -n sap create secret generic forgejo-agent \
  --from-literal=FORGEJO_TOKEN="$TOKEN" \
  --from-literal=FORGEJO_URL=http://forgejo.forgejo.svc:3000 \
  --dry-run=client -o yaml \
  | kubeseal --cert talos/clusters/homelab/sealed-secrets.crt -o yaml \
  > k8s/apps/sap-provisioner/forgejo-agent.sealed.yaml
unset TOKEN ADMIN_PW
```

Commit `k8s/apps/sap-provisioner/forgejo-agent.sealed.yaml` (signed). Rotation
= rerun step 5 with a new `--token-name` and delete the old token
(`DELETE /api/v1/users/agent/tokens/sandbox` as admin). The secret lives in
`sap`, where the provisioner's `-outfit` attaches it to agent children
(e6pu.3; the `ai` copy went with the shell git path).

Check from inside the cluster (`/api/v1/user` needs `read:user`, which
this token deliberately lacks — ask for a repo it can write instead;
the pod must satisfy PSS restricted, hence the overrides):

```sh
T=$(kubectl -n ai get secret forgejo-agent -o jsonpath='{.data.FORGEJO_TOKEN}' | base64 -d)
kubectl -n ai run --rm -i tokcheck --image=curlimages/curl --restart=Never --env="T=$T" \
  --overrides='{"spec":{"securityContext":{"runAsNonRoot":true,"runAsUser":100,"seccompProfile":{"type":"RuntimeDefault"}},"containers":[{"name":"tokcheck","image":"curlimages/curl","env":[{"name":"T","value":"'"$T"'"}],"command":["sh","-c","curl -sS -o /dev/null -w \"%{http_code}\\n\" -H \"Authorization: token $T\" http://forgejo.forgejo.svc:3000/api/v1/repos/agents/scratch"],"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}}}]}}'
```

Expect `200` (verified 2026-10-11). The `ai` namespace has the Secret
only once the sealed-secrets controller has unsealed the committed file
(ArgoCD polls ~3 min; do not hand-apply — notes.md 2026-10-10).
