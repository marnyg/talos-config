# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Agent workloads on the cluster — the sandbox layer (ADR-0035)
and the first agent on it are both live (2026-10-10): pi runs as a
plain Job under `RuntimeClass kata`, batch or attachable, started from
the laptop with `scripts/agent-run.sh`. The next step is using it for
real work and letting that decide what hurts first: workspace shape
(emptyDir vs volume, virtio-fs cost `5h0j`), egress, budget.

**Toward goal:** "Sovereign-actor protocol at the center" (`goals.md`)
— the harness image is the first agent-as-child candidate: the k8s
driver already births Jobs with a deadline; adding the child beat to
this image and `runtimeClassName` to the driver makes an agent a
sandbox with a lease. Budget stays the open gap until M5 (`0bc.5`).

**Out of scope:** an orchestrator or UI (kagent 1.0 / ax, decision
`6d0u`) — the null option is the answer until real use says otherwise;
kata tuning (`5h0j`) before an agent feels it; NetworkPolicy for the
sandbox before a CNI that enforces it; a second control plane (`bhui`,
`dsuj`); KMS onto 443 (`os8s`); a mesh CA (`9z4e`).
