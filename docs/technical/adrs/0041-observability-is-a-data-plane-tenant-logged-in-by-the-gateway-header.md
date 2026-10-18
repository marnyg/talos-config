# ADR-0041: Observability is a data-plane tenant; Grafana logs a device in by the gateway header

- Status: Accepted 2026-10-18 (owner). Live since 2026-10-16 (epic
  `talos-config-xhk3`): kube-prometheus-stack 92.10.0, Loki 7.3.0 +
  Alloy, metrics-server under `k8s/apps/monitoring`; four Talos
  control-plane targets scraped off loopback.
- Date: 2026-10-18 (recorded after the build; task `talos-config-drou`)
- Related: ADR-0027 (the hub's gitops/storage rows — the watcher
  outside the failure domain; its option B was "in-cluster Prometheus",
  declined *for that job*), ADR-0032 (the group gate; the Tailscale
  `proxy-to-grafana` pattern it rejected as a *gate* is what Grafana
  uses here as a *username*), ADR-0033 (https on the mesh, Grafana's
  `root_url`), ADR-0011/0029 (Longhorn classes), invariant 2 (data-plane
  exception), invariant 5; spike `talos-config-i1il`; ADR-0042 (the
  page these alerts become)

## Context and Problem Statement

Until 2026-10-16 the cluster had no metrics at all — not even
metrics-server — and every incident in `notes.md` (w1 off for 8 days,
bulk replicas faulted for 12, nas1 rebuilding for hours) was found by
a human noticing something was slow. ADR-0027 gave the hub two rows
(`gitops`, `storage`) read from outside the cluster, deliberately
minimal, with "the cluster gains a monitoring stack" named as a
revisit trigger. The stack has now landed. Two things need recording:
where it sits in the trust model, and how the owner gets into Grafana.

## Decision Drivers

- Invariant 2: git is the truth for the control plane; a server may
  hold state only as data plane. A TSDB and a log store are state.
- Invariant 5: nothing new public. Prometheus, Alertmanager and Loki
  have no auth of their own; Grafana has a login.
- ADR-0032: a bare `X-Mesh-*` header is never *authority* — any pod can
  forge one toward ingress-nginx; only the gateway-signed token is.
- The stack must not take over ADR-0027's job: the hub watches from
  outside the failure domain, Prometheus from inside it.
- Dashboards, datasources and rules should be git, not clicks.

## Considered Options

### Option A: in-cluster kube-prometheus-stack + Loki as a data-plane tenant; Grafana behind the group gate, header as username (chosen)

One namespace, PSS privileged (node-exporter, Alloy on the host), every
pod that can run restricted does. Prometheus 20Gi/15d, Loki 30Gi/14d,
Grafana 2Gi, Alertmanager 1Gi, all Longhorn nvme, off nas1. Only
Grafana has an Ingress; the gate is the controller's global
`auth-url` → `/authz` (`admins`). Grafana's `auth.proxy` then takes
`X-Mesh-Name` as the username with `auto_sign_up` and org role Admin.
Dashboards are ConfigMaps labelled `grafana_dashboard=1` in any
namespace, picked up by the sidecar; datasources and the Alertmanager
config likewise from values.

- Pros: the gate (ADR-0032's signed token) decides *whether* a request
  reaches Grafana; the header only decides *as whom*, after the gate
  already proved `admins`. Forging the header from a pod reaches
  `grafana:80` — a bypass every `auth-url`-gated Service already has,
  bounded by the CNI's missing NetworkPolicy, not by this choice. A
  wiped volume comes back configured and empty.
- Cons: a device, not a person, is the principal (ArgoCD's
  `-token-client` reasoning: dashboards carry no per-person state worth
  splitting). In-cluster, so it shares the failure domain it watches;
  the hub's rows stay for the absence cases.

### Option B: Grafana with SIWE→OIDC (the person gate)

- Pros: a person, not a device, in the audit log.
- Cons: a wallet prompt to look at a graph, on a device the network
  already admitted — exactly the friction `i1il` was opened for.
  Nothing in Grafana is per-person.

### Option C: metrics on the hub (ADR-0027 grown)

- Cons: the hub is an ephemeral-key actor and holds no durable state
  (invariant 2, actor-owned state); a TSDB is the opposite of that. The
  hub stays the observer of absence, not the home of history.

### Option D: hosted Grafana Cloud / a SaaS sink

- Cons: a hosted account in the path of every dashboard and log line
  (invariant 3's spirit); a public egress of cluster internals.

## Decision Outcome

Chosen: **Option A**. The stack is a **data-plane tenant**: its stores
are a projection of a cluster that is itself re-derivable, losing them
loses history, not a capability, and their durability is Longhorn's
replication-and-backup problem (`bsj`). Everything that makes the
stack *configured* — dashboards, datasources, scrape targets, rules,
Alertmanager routes — is git.

Grafana's login is the gate plus the header: **the admins gate is the
whole login, the header is only the name**. That is the Tailscale
`proxy-to-grafana` shape, admissible here precisely because the
*decision* was already made on a signed token one hop earlier
(ADR-0032); the header never decides, it labels.

### Consequences

- `k8s/apps/monitoring` is one directory, one namespace, PSS
  privileged as a ceiling (node-exporter, Alloy); `namespace.yaml`
  says why.
- The convention for the rest of the repo: **exposing metrics = a
  ServiceMonitor next to the app**; the Prometheus selectors are
  cross-namespace for that. A dashboard lives next to the app it
  describes (`dashboard.yaml`, `grafana_dashboard=1`).
- Talos binds controller-manager/scheduler/kube-proxy metrics to the
  node address and etcd to `:2381` (`talos/clusters/homelab/
  cluster.yaml`); etcd is scraped by static address because the
  chart's `kubeEtcd.endpoints` renders a bare `Endpoints` ArgoCD 3
  never creates.
- Prometheus and Alertmanager are unexposed; Grafana's Explore and
  Alertmanager views are the UI. Alerts become pages through ADR-0042.
- ADR-0027's rows stay: the hub is the watcher *outside* the failure
  domain; Prometheus cannot tell you that cp1 is off.
- Any pod can present itself to `grafana:80` as `admins` by header.
  Closing that is NetworkPolicy (CNI), not Grafana; thread on the epic.

### Confirmation

- Correct when: a wiped `monitoring` namespace resyncs to the same
  dashboards/datasources with empty stores; an `admins` device opens
  `grafana.gw.mesh.internal` with no login and lands as its mesh
  name; a non-`admins` device gets 403; a curl from a pod with a
  forged `X-Mesh-Name` toward ingress-nginx gets 401 (no token).
- Revisit if: a dashboard or alert ever needs per-person state (then
  Option B for Grafana); the CNI gains NetworkPolicy (close the pod
  bypass); the stack is asked to *act* on what it sees (a control
  loop is a new ADR, as ADR-0027 says of the hub).
