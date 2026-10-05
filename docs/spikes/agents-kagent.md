# Spike `talos-config-kanr` — agents-as-pods on a GPU-less cluster

_2026-10-06. Memo only; nothing here deploys. Draft manifests for the
recommended step sit in `agents-kagent/` beside this file._

## Question

With no GPU and no local inference — OpenRouter as the only model
provider, through the key the owner already holds — is **kagent**, or
**google/ax**, or **neither** the right runtime for running agents as
pods on this Talos cluster? And how would such a pod relate to the
sovereign-actor protocol: child actor, or plain workload?

## Candidates

- **kagent** — kagent-dev/kagent, CNCF sandbox, Apache-2.0. Two live
  lines: **0.10.3** (2026-10-02; controller + Next.js UI + PostgreSQL +
  per-`Agent` Deployments on its Python/Go ADK) and **1.0.0-alpha8**
  (2026-10-05; a rewrite on *Agent Substrate* — gVisor/microVM sandboxes,
  needs k8s ≥ 1.37 + `certificates.k8s.io/v1beta1`, rustfs, no in-place
  upgrade from 0.10). Only 0.10.x can run here (cluster is k8s 1.32.3,
  `docs/technical/deployed-state.md:35`).
- **google/ax** — github.com/google/ax, "Google's open agentic
  orchestration runtime", Go, 13k stars. It is the *Agent Executor*
  announced for Agent Substrate: a batch orchestrator ("billions of
  tasks") with `ax.io/v1alpha1` `Task`/`Workspace`/`Model` documents
  applied by its own `ax` CLI to its own gRPC control plane backed by
  Redis — **not** Kubernetes CRDs. Needs Substrate; deploys via
  `make deploy` + `ko` into your registry; default runner is Antigravity;
  `Model.provider` is `google` or `anthropic` only. README warns of
  breaking changes before any stable release. _(Not `ax-llm/ax`, the
  TypeScript DSPy port — a different project.)_
- **Null option** — one Deployment in `ai` running any agent framework
  (ADK, pi, a Claude-Code-style harness) with the key from a
  SealedSecret and an Ingress behind the wallet gate: exactly the
  SillyTavern shape (`k8s/apps/sillytavern/`).

## Comparison

| Axis | kagent 0.10.3 | google/ax | Null option (Deployment + framework) |
|---|---|---|---|
| OpenRouter | Yes: `ModelConfig{provider: OpenAI, openAI.baseUrl: https://openrouter.ai/api/v1}` — the documented BYO OpenAI-compatible path (`docs/…/byo-openai`); `defaultHeaders` for OpenRouter's optional attribution headers. Any OpenRouter model id in `spec.model`. | No: `provider: google \| anthropic` (`docs/manifests.md:104,124`); no base-URL field. Would need a fork or an OpenAI-shim sidecar. | Yes, trivially — whatever the framework's `OPENAI_BASE_URL` is. |
| Deploy shape under raw-YAML ArgoCD | Two OCI Helm charts (`oci://ghcr.io/kagent-dev/kagent/helm/{kagent-crds,kagent}`) → two `Application`s like `k8s/apps/longhorn/application.yaml`, plus `ModelConfig`/`Agent` CRs as raw YAML (root app has `SkipDryRunOnMissingResource`, `k8s/apps/kubevirt/kubevirt-cr.yaml:4`). `unverified:` first OCI chart in this repo — ArgoCD 3.4.5 needs a `helm` repo Secret with `enableOCI: "true"`. | None: no chart, no published images, control plane is `ko`-built; its objects are not k8s resources, so ArgoCD cannot own them. Substrate itself is a CRD+operator stack sized for a dev kind cluster. | One directory of plain manifests — the existing pattern. |
| UI / auth | Web UI (chat, agents, models). Controller auth modes: `unsecure` (trusts `X-User-Id` header, else `admin@kagent.dev`) or `trusted-proxy` (JWT from oauth2-proxy via `Authorization`) (`values.yaml:173-179`). Either way the wallet gate on the Ingress is the boundary; `unsecure` behind it = single-user, same posture as SillyTavern. `trusted-proxy` against `siwe-oidc` is a later refinement, `unverified:`. | No UI; CLI + gRPC (`ax ssh` into sandboxes). Would need its own gate. | Whatever the framework ships; gated the same way. |
| Footprint (requests) | Controller 100m/128Mi, UI 100m/256Mi, bundled Postgres 250m/256Mi; kmcp, kagent-tools and **ten** bundled agent Deployments (50m/128Mi each) default-on — disable them. Minimal install ≈ 0.45 CPU / 0.65 Gi requests, 3 pods + 1 per `Agent`. `unverified:` node headroom — cluster unreachable from this session; w1 is off, nas1 is 4×800 MHz / 7.5 GiB. | Substrate (api, atenet router, Postgres, rustfs) + Redis + ax control plane + gVisor worker pods: heavier than the whole current `ai` namespace. | One pod. |
| State / storage class | PostgreSQL is mandatory (sessions, tasks, history). Bundled instance is "demo only" with hardcoded `kagent/kagent` creds; PVC on `longhorn` (default class, ADR-0029) — data-plane state, same class as SillyTavern's chats. | Redis + Substrate's Postgres/rustfs + per-task snapshot volumes. | A PVC on `longhorn` if the framework keeps anything. |
| Secret handling (inv. 8) | `apiKeySecret`/`apiKeySecretKey` reference a Secret in the same namespace → SealedSecret, `k8s/apps/jackett/sealed-secret.yaml` pattern. `apiKeyPassthrough` (bearer from the A2A caller) exists for later. | `Model.secretKey` → Secret; same. | `envFrom` a SealedSecret. |
| Maturity | 0.10.x is the shipped line but is being superseded: 1.0 alphas every few days, CRD group changes, no in-place upgrade. CI tests k8s 1.35 only; 1.32 is three minors back (`unverified:` whether it runs). | Pre-release, "major breaking changes" promised, Google-internal cadence. | Boring; nothing to outgrow. |

## Agents and the protocol

Today the k8s driver (`actors/driver/k8s/k8s.go`) births a child as a
**Job**: image by digest, the intro as an env var, `activeDeadlineSeconds`
as the lease deadline; the child mints its own key, knocks on the
parent's `#birth`, gets a starter kit, and self-lapses when it holds no
unexpired `(P, #renew)` chain. An **agent as child actor** is therefore
an *image* question, not a runtime question: an agent image that embeds
the child beat (`actors/child`) is a protocol child like any other —
reachable as facets over the mesh, authority bounded by the certs it is
handed, lifetime bounded by the lease. The LLM call is just what the
child does with its CPU time.

Mapping kagent's objects onto that:

- `Agent` ≈ the child *recipe* (prompt, tools, model ref, image). It
  compiles to a long-running **Deployment owned by kagent's controller**
  — no lease, no deadline, no self-lapse. That is the structural
  mismatch: kagent supervises by reconciliation, the protocol supervises
  by funding. An `Agent` can be a *workload* in the cluster; it cannot
  be a *child* without a shim that turns a lease into an `Agent` CR and
  deletes it on lapse.
- `ModelConfig` ≈ a data-plane credential binding. The protocol has no
  slot for it — correctly: v0 ships no secrets to children on low-trust
  providers (`sovereign-actor-protocol.md`, trust axiom). Our own
  cluster is the high-trust case, so the key would reach the pod from a
  SealedSecret, never through the intro.
- kagent's A2A endpoint ≈ a facet (`#invoke`), but authorised by
  kagent's auth mode, not by a delegation cert. `apiKeyPassthrough`
  hints at the bridge: a facet handler could verify a chain and forward
  a budgeted key — not built.
- **Budget** is the real gap on both sides. kagent meters nothing;
  the protocol's v0 cert says "may, never how much" — counted caveats
  (spend caps) are M5 money (`0bc.5`). Until then an agent's OpenRouter
  spend is bounded only by OpenRouter's own key limits. Set one there.

Conclusion: for this spike an agent is a **plain workload**. "Agent as
child" is a later experiment on the null option (child beat + harness in
one image, spawned by the provisioner), not on kagent.

## Invariant check

1. **Identity/membership** — untouched. The OpenRouter key is a
   **data-plane credential**, not an identity: it authorises spend
   against a vendor, never a user or member. No third-party account
   enters any auth path; the UI is gated by the wallet via the Ingress.
2. **Git is source of truth** — Applications, CRs, Ingress, SealedSecret
   in git; kagent's Postgres (sessions/history) is data-plane state in
   the Longhorn exception, like SillyTavern's chats. Nothing control-plane
   lives in it.
5. **Single public entrypoint** — `kagent.gw.mesh.internal` is reachable
   only through the gateway; no new port, no public surface. Egress to
   `openrouter.ai` is outbound only.
8. **Secrets in memory only** — key arrives as a SealedSecret, lands in
   the agent pod's env; never in git plaintext, never in an image.
   Caveat to state: kagent's bundled Postgres has a hardcoded password
   (in-cluster only; a real credential if the DB is ever exposed).

## Recommendation

**Trial kagent 0.10.3 as a workload in `ai`, time-boxed; rule out
google/ax now.** ax fails three hard filters (no OpenRouter, not
GitOps-deployable, needs Substrate on a 1.32 cluster). kagent is the
only candidate that gives declarative agents + a UI + MCP tools for one
Helm Application and fits every existing pattern (Helm Application,
SealedSecret, wallet-gated Ingress, Longhorn PVC). Its cost is churn:
1.0 will force a re-evaluation within months, so treat 0.10 as a trial
whose exit is `git rm` of one directory. If the trial shows the UI is
the only thing used, fall back to the null option.

**First step (one session):**

1. Review `agents-kagent/` (Application pair, OCI repo Secret,
   SealedSecret placeholder, `ModelConfig`, one `Agent`, Ingress).
2. Create an OpenRouter key *with a spend limit* for the cluster; seal
   it: `kubeseal --format yaml < secret.yaml` with
   `OPENROUTER_API_KEY` in namespace `ai`; replace `PLACEHOLDER`.
3. `git mv docs/spikes/agents-kagent k8s/apps/kagent`, commit (signed),
   push; watch ArgoCD sync the CRDs app before the CRs settle.
4. Verify: `kagent-ui` reachable at `kagent.gw.mesh.internal` only after
   the wallet sign-in; one chat turn through `openrouter`; pod requests
   fit the nodes. Record `unverified:` cells as verified or not.
5. Decide: keep (file `kagent-tools` + `trusted-proxy` follow-ups) or
   revert in one commit.

## Open questions

- ArgoCD 3.4.5 + anonymous OCI chart pull with a `helm` repo Secret and
  `enableOCI: "true"` — works in theory; first use here.
- kagent 0.10.3 on k8s 1.32.3: three minors below its tested 1.35.
- Does the Deployment kagent renders per `Agent` pass PodSecurity
  `restricted` without the `deployment.securityContext` overrides the
  draft sets? Unverified; the overrides are set defensively.
- `trusted-proxy` via oauth2-proxy → `siwe-oidc`: the Ingress would need
  `auth-response-headers: Authorization` and oauth2-proxy
  `set-authorization-header`; is kagent's `userIdClaim` satisfiable from
  a SIWE-derived JWT (`sub` = wallet address)?
- OpenRouter attribution headers (`HTTP-Referer`, `X-Title`) via
  `defaultHeaders` — harmless, optional, left in as comments.
- Agent-as-child: should the `agent` kind enter the protocol domain
  model, or is it just "a child whose image happens to call an LLM"?
  This memo argues the latter until budget caveats exist.
- Budget: the only spend bound is OpenRouter's key limit. Does the
  owner want a per-key limit documented as a norm (pre-M5)?
