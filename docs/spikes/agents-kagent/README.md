# Draft manifests — kagent 0.10.3 trial (spike `talos-config-kanr`)

Parked under `docs/spikes/` on purpose: the root ArgoCD `apps`
Application syncs `k8s/apps` recursively, so nothing here deploys until
the directory is moved. See `../agents-kagent.md` for the memo.

Files, in sync order:

| File | What |
|---|---|
| `argocd-repo-oci.yaml` | Helm repository Secret telling ArgoCD that `ghcr.io/kagent-dev/kagent/helm` is an OCI registry (no credentials — public charts). |
| `application-crds.yaml` | `kagent-crds` chart 0.10.3 → `ai`. |
| `application.yaml` | `kagent` chart 0.10.3 → `ai`, trimmed: no bundled agents, no kmcp/tools, `providers: null`, Postgres on `longhorn`. |
| `sealed-secret.yaml` | `openrouter` SealedSecret with `PLACEHOLDER` ciphertext. |
| `modelconfig.yaml` | `ModelConfig openrouter` — OpenAI-compatible provider at `https://openrouter.ai/api/v1`. |
| `agent.yaml` | One declarative `Agent` (`scout`), no tools, restricted security context. |
| `ingress.yaml` | `kagent.gw.mesh.internal` behind the wallet gate. |
| `kustomization.yaml` | Only so `kubectl kustomize .` validates the set; harmless if moved along. |

Promote:

```sh
# 1. seal the real key (never commit secret.yaml)
kubectl create secret generic openrouter -n ai --dry-run=client \
  --from-literal=OPENROUTER_API_KEY=... -o yaml > /tmp/secret.yaml
kubeseal --format yaml < /tmp/secret.yaml   # paste encryptedData into sealed-secret.yaml
# 2. move and commit
git mv docs/spikes/agents-kagent k8s/apps/kagent && git rm k8s/apps/kagent/README.md
```
