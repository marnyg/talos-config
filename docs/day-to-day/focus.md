# Current Focus

<!-- Forward-looking. Replace when focus shifts. Keep to ~20 lines.
     The link between current work and a higher-order goal. -->

**Now:** Agent workloads on the cluster, sandbox first (ADR-0035,
2026-10-08): every node carries Kata Containers and `RuntimeClass
kata` gives any pod its own guest kernel for one line of YAML. The
orchestrator question is reopened from that footing — the next step is
one agent as a plain, sandboxed workload (`tj7c`), not a framework.

**Toward goal:** "Sovereign-actor protocol at the center" (`goals.md`)
— an agent that runs code is the first child whose image calls an LLM;
a Job under kata with a deadline is already the shape the k8s driver
births children in. Budget stays the open gap until M5 (`0bc.5`).

**Next candidates** (owner to pick):
- `tj7c` harness image under kata (OpenRouter, sealed key, gated
  entry).
- `bsj` backup target; `dsuj` waits on the Windows data copy.

**Out of scope:** kagent 0.10 / ax / Agent Substrate until kagent 1.0
is GA (decision `6d0u`); kata tuning (`5h0j`) before an agent feels
it; a second control plane (`bhui`, `dsuj`); KMS onto 443 (`os8s`); a
mesh CA (`9z4e`).
