#!/usr/bin/env bash
# Run one sandboxed agent (k8s/apps/sandbox/agent.yaml, tj7c): render a
# Job from the suspended CronJob's template and create it. The
# kubeconfig is the gate — it reaches the API over the mesh only.
#
#   scripts/agent-run.sh "<prompt>"       batch: pi -p; follows the log
#   scripts/agent-run.sh -f prompt.md     batch, prompt from a file
#   scripts/agent-run.sh                  interactive: tmux + pi; attaches
#                                         (detach C-b d; reattach with the
#                                         printed kubectl exec)
#
#   -m MODEL     pi model id under openrouter (default: the template's)
#   -d SECONDS   activeDeadlineSeconds for this run (default: template's)
#   -n NAME      Job name (default agent-<utc timestamp>)
#   -q           create only; do not follow / attach
#
# The Job is deleted by ttlSecondsAfterFinished; `kubectl delete job -n ai
# <name>` ends a run early (the deadline otherwise).
set -euo pipefail
ns=ai
model="" deadline=0 name="" quiet=0 file=""
while getopts "m:d:n:f:q" opt; do
    case $opt in
        m) model=$OPTARG ;;
        d) deadline=$OPTARG ;;
        n) name=$OPTARG ;;
        f) file=$OPTARG ;;
        q) quiet=1 ;;
        *) exit 2 ;;
    esac
done
shift $((OPTIND - 1))
prompt="$*"
if [ -n "$file" ]; then
    [ -z "$prompt" ] || { echo "either -f FILE or a prompt argument, not both" >&2; exit 2; }
    prompt=$(cat "$file")
fi
[ -n "$name" ] || name="agent-$(date -u +%Y%m%d-%H%M%S)"

kubectl -n "$ns" get cronjob agent -o json \
    | jq --arg name "$name" --arg ns "$ns" --arg prompt "$prompt" --arg model "$model" --argjson deadline "$deadline" '
        .spec.jobTemplate
        | .apiVersion = "batch/v1" | .kind = "Job"
        | .metadata = {name: $name, namespace: $ns, labels: {app: "agent"},
                       annotations: {"agent.talos-config/mode": (if $prompt == "" then "interactive" else "batch" end)}}
        | (if $deadline > 0 then .spec.activeDeadlineSeconds = $deadline else . end)
        | .spec.template.spec.containers[0].env |= (
            map(select(.name != "AGENT_PROMPT" and (.name != "AGENT_MODEL" or $model == "")))
            + (if $prompt != "" then [{name: "AGENT_PROMPT", value: $prompt}] else [] end)
            + (if $model != "" then [{name: "AGENT_MODEL", value: $model}] else [] end))' \
    | kubectl create -f -

[ "$quiet" -eq 0 ] || exit 0

echo "waiting for the sandbox to boot…" >&2
kubectl -n "$ns" wait --for=condition=Ready pod -l "batch.kubernetes.io/job-name=$name" --timeout=180s >&2 \
    || { kubectl -n "$ns" get pod -l "batch.kubernetes.io/job-name=$name" >&2; exit 1; }
pod=$(kubectl -n "$ns" get pod -l "batch.kubernetes.io/job-name=$name" -o jsonpath='{.items[0].metadata.name}')

if [ -n "$prompt" ]; then
    exec kubectl -n "$ns" logs -f "$pod"
fi
echo "reattach later: kubectl exec -it -n $ns $pod -- tmux attach" >&2
exec kubectl -n "$ns" exec -it "$pod" -- tmux attach
