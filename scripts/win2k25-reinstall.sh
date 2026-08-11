#!/usr/bin/env nix-shell
#!nix-shell -i bash -p kubectl
#
# Reinstall the win2k25 guest from scratch, hands-free.
#
# Thin wrapper: fires the in-cluster trigger (the suspended CronJob in
# k8s/apps/vms/win2k25-reinstall-trigger.yaml), which deletes the
# system DataVolume + running instance. virt-controller recreates both,
# the blank disk boots the no-prompt ISO, and the sealed answer file
# does the entire install — including enabling RDP.
#
# DESTROYS the Windows system disk. ISO and answer file are untouched.
#
# ~25 min later:
#   xfreerdp3 /v:cp1.mesh.internal:30389 /u:Administrator
# password:
#   kubectl -n vms get secret win2k25-admin -o jsonpath='{.data.password}' | base64 -d
set -euo pipefail

NS=vms
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export KUBECONFIG="${KUBECONFIG:-$REPO_ROOT/kubeconfig}"

echo ">>> This WIPES the win2k25 system disk and reinstalls Windows."
echo ">>> Ctrl-C within 5s to abort."
sleep 5

JOB="win2k25-reinstall-$(date +%s)"
kubectl -n "$NS" create job --from=cronjob/win2k25-reinstall "$JOB"
kubectl -n "$NS" wait --for=condition=complete "job/$JOB" --timeout=120s
kubectl -n "$NS" logs "job/$JOB"

echo ">>> Reinstall triggered. Unattended install running (~25 min)."
echo ">>> Watch:  kubectl get dv,vmi -n $NS   (or virtctl vnc win2k25 -n $NS)"
