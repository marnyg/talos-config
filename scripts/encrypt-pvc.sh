#!/usr/bin/env bash
# Move a bound RWO PVC onto the `longhorn-encrypted` class in place:
# same claim name, ONE copy, old volume kept as a safety net.
#
#   scripts/encrypt-pvc.sh migrate <namespace> <pvc>
#   scripts/encrypt-pvc.sh finish  <namespace> <pvc>
#
# A PVC's storageClassName is immutable and LUKS cannot be slid under a
# live filesystem, so "encrypting" a volume is a copy. The trick that
# keeps the claim name (2026-10-23, from the files/transfer rebind):
#
#   migrate:
#     1. pause the owning ArgoCD app's automation (selfHeal would undo
#        the scale-down within seconds); scale every Deployment /
#        StatefulSet mounting the PVC to 0 and wait for its pods
#     2. set the old PV to Retain (the plain class provisions Delete)
#     3. create <pvc>-enc on longhorn-encrypted, same size; rsync the
#        old volume into it from a Job that mounts both; verify with a
#        second, itemizing dry run that must print nothing
#     4. delete <pvc>-enc (its PV is Retain -> Released), clear its
#        claimRef; delete the old <pvc> (its PV stays, labeled
#        encrypt-pvc/old=<date> for later cleanup)
#     5. recreate <pvc> on longhorn-encrypted with volumeName = the
#        encrypted PV -> Bound, same data, no second copy
#     6. label the new Longhorn Volume into the `backup` group (the
#        class's recurringJobSelector only fires at CSI provisioning,
#        which step 3 did under the OTHER claim name - belt and braces)
#     Leaves the app paused and scaled to 0. Then commit the manifest
#     (storageClassName: longhorn-encrypted, or drop the field) and:
#   finish:
#     restores the app's automation; selfHeal scales the workload back
#     up from git.
#
# Refuses a PVC that is RWX, Block, unbound, or mounted by a KubeVirt
# VMI (the VM disks stay on the plain class by decision). Old PVs are
# NOT deleted here: `kubectl get pv -l encrypt-pvc/old` lists them once
# the migrated apps have run for a while.
set -euo pipefail

mode=${1:-}; ns=${2:-}; pvc=${3:-}
[ -n "$mode" ] && [ -n "$ns" ] && [ -n "$pvc" ] || { sed -n 2,5p "$0"; exit 2; }

ENC_CLASS=longhorn-encrypted
TODAY=$(date -u +%Y-%m-%d)

app_of() {
  kubectl -n "$ns" get pvc "$pvc" -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/tracking-id}' 2>/dev/null | cut -d: -f1
}

pause_app() {
  local app=$1
  if kubectl -n argocd get app "$app" -o jsonpath='{.spec.syncPolicy.automated}' | grep -q .; then
    kubectl -n argocd patch app "$app" --type=json -p '[{"op":"remove","path":"/spec/syncPolicy/automated"}]' >/dev/null
    echo "paused argocd app $app"
  fi
}

resume_app() {
  local app=$1 head
  # Resume only once ArgoCD has fetched the commit that carries the
  # new storageClassName: otherwise selfHeal syncs the cached revision
  # against the recreated PVC, fails on the immutable field, and the
  # retry loop keeps that stale operation alive past the next refresh.
  head=$(git rev-parse HEAD)
  kubectl -n argocd annotate app "$app" argocd.argoproj.io/refresh=hard --overwrite >/dev/null
  for i in $(seq 1 30); do
    [ "$(kubectl -n argocd get app "$app" -o jsonpath='{.status.sync.revision}')" = "$head" ] && break
    sleep 3
  done
  # Drop any operation still retrying an older revision.
  kubectl -n argocd patch app "$app" --type=json -p '[{"op":"remove","path":"/operation"}]' >/dev/null 2>&1 || true
  kubectl -n argocd patch app "$app" --type=merge \
    -p '{"spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true}}}}' >/dev/null
  echo "resumed argocd app $app at $(git rev-parse --short HEAD)"
}

workloads_using() {
  for kind in deployment statefulset; do
    kubectl -n "$ns" get "$kind" -o json \
      | jq -r --arg p "$pvc" --arg k "$kind" \
        '.items[] | select(.spec.template.spec.volumes[]?.persistentVolumeClaim.claimName==$p) | "\($k)/\(.metadata.name)"'
  done
}

case "$mode" in
migrate)
  # --- preflight -------------------------------------------------------
  json=$(kubectl -n "$ns" get pvc "$pvc" -o json)
  phase=$(jq -r .status.phase <<<"$json")
  mode_=$(jq -r '.spec.volumeMode // "Filesystem"' <<<"$json")
  access=$(jq -r '.spec.accessModes|join(",")' <<<"$json")
  size=$(jq -r .spec.resources.requests.storage <<<"$json")
  oldpv=$(jq -r .spec.volumeName <<<"$json")
  oldsc=$(jq -r .spec.storageClassName <<<"$json")
  tracking=$(jq -r '.metadata.annotations["argocd.argoproj.io/tracking-id"] // ""' <<<"$json")
  [ "$phase" = Bound ] || { echo "refusing: $ns/$pvc is $phase"; exit 1; }
  [ "$mode_" = Filesystem ] || { echo "refusing: volumeMode $mode_"; exit 1; }
  [ "$access" = ReadWriteOnce ] || { echo "refusing: accessModes $access"; exit 1; }
  [ "$oldsc" != "$ENC_CLASS" ] || { echo "already on $ENC_CLASS"; exit 0; }
  if kubectl -n "$ns" get vmi -o json 2>/dev/null | jq -e --arg p "$pvc" \
       '.items[] | select(.spec.volumes[]? | (.persistentVolumeClaim.claimName==$p or .dataVolume.name==$p))' >/dev/null; then
    echo "refusing: a VMI mounts $pvc"; exit 1
  fi
  app=$(app_of)
  echo "== $ns/$pvc  size=$size  pv=$oldpv  class=$oldsc  app=${app:-<none>}"

  # --- 1. pause + scale down ------------------------------------------
  [ -n "$app" ] && pause_app "$app"
  mapfile -t wl < <(workloads_using)
  for w in "${wl[@]}"; do kubectl -n "$ns" scale "$w" --replicas=0 >/dev/null; echo "scaled $w -> 0"; done
  for i in $(seq 1 60); do
    kubectl get pods -A -o json | jq -e --arg ns "$ns" --arg p "$pvc" \
      '.items[] | select(.metadata.namespace==$ns and (.spec.volumes[]?.persistentVolumeClaim.claimName==$p))' >/dev/null 2>&1 || break
    sleep 3
  done
  kubectl get pods -A -o json | jq -e --arg ns "$ns" --arg p "$pvc" \
    '.items[] | select(.metadata.namespace==$ns and (.spec.volumes[]?.persistentVolumeClaim.claimName==$p))' >/dev/null 2>&1 \
    && { echo "pods still mount $pvc; aborting (app paused, workloads at 0)"; exit 1; }

  # --- 2. old PV to Retain -------------------------------------------
  kubectl patch pv "$oldpv" --type=merge -p '{"spec":{"persistentVolumeReclaimPolicy":"Retain"}}' >/dev/null
  echo "old pv $oldpv -> Retain"

  # --- 3. temp encrypted PVC + copy ----------------------------------
  tmp="$pvc-enc"
  kubectl apply -f - <<EOF >/dev/null
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: $tmp, namespace: $ns}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: $ENC_CLASS
  resources: {requests: {storage: "$size"}}
EOF
  kubectl -n "$ns" wait pvc/"$tmp" --for=jsonpath='{.status.phase}'=Bound --timeout=120s >/dev/null
  newpv=$(kubectl -n "$ns" get pvc "$tmp" -o jsonpath='{.spec.volumeName}')
  echo "temp pvc $tmp bound to $newpv"

  job="encrypt-$pvc"
  kubectl -n "$ns" delete job "$job" --ignore-not-found >/dev/null
  kubectl apply -f - <<EOF >/dev/null
apiVersion: batch/v1
kind: Job
metadata: {name: $job, namespace: $ns}
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: rsync
          image: alpine:3.20
          command: ["sh","-ec"]
          args:
            - |
              apk add --no-cache rsync >/dev/null
              rsync -aHAX --numeric-ids --delete --exclude=/lost+found /src/ /dst/
              sync
              echo "--- verify (must be empty) ---"
              rsync -aHAXn --numeric-ids --delete --exclude=/lost+found --itemize-changes /src/ /dst/ | tee /tmp/diff
              [ ! -s /tmp/diff ]
              echo "--- ok: \$(find /dst -xdev | wc -l) entries, \$(du -sh /dst | cut -f1)"
          volumeMounts:
            - {name: src, mountPath: /src, readOnly: true}
            - {name: dst, mountPath: /dst}
          # root with default caps: rsync -aHAX needs chown/xattr rights
          # on /dst. Baseline PSS allows this (no privilege, no host).
          securityContext: {allowPrivilegeEscalation: false, seccompProfile: {type: RuntimeDefault}}
      volumes:
        - {name: src, persistentVolumeClaim: {claimName: $pvc, readOnly: true}}
        - {name: dst, persistentVolumeClaim: {claimName: $tmp}}
EOF
  echo "copying (job $job; first attach of an encrypted volume takes ~90 s)..."
  if ! kubectl -n "$ns" wait job/"$job" --for=condition=complete --timeout=45m 2>/dev/null; then
    echo "copy job did not complete:"; kubectl -n "$ns" logs job/"$job" --tail=20 || true
    echo "nothing destroyed: $ns/$pvc still bound to $oldpv (Retain); temp $tmp on $newpv; app paused."
    exit 1
  fi
  kubectl -n "$ns" logs job/"$job" | tail -3

  # --- 4. release both claims ----------------------------------------
  # The Completed pod still counts for pvc-protection: remove the Job
  # (and so the pod) first or the temp PVC sits Terminating forever.
  kubectl -n "$ns" delete job "$job" --wait=true >/dev/null
  kubectl -n "$ns" delete pvc "$tmp" --timeout=120s >/dev/null
  kubectl patch pv "$newpv" --type=json -p '[{"op":"remove","path":"/spec/claimRef"}]' >/dev/null
  kubectl label pv "$oldpv" "encrypt-pvc/old=$TODAY" "encrypt-pvc/claim=$ns.$pvc" --overwrite >/dev/null
  kubectl -n "$ns" delete pvc "$pvc" --timeout=60s >/dev/null
  echo "released: old $oldpv (Retain, labeled encrypt-pvc/old=$TODAY), new $newpv Available"

  # --- 5. rebind under the original name -----------------------------
  kubectl apply -f - <<EOF >/dev/null
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $pvc
  namespace: $ns
  annotations: {argocd.argoproj.io/tracking-id: "$tracking"}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: $ENC_CLASS
  volumeName: $newpv
  resources: {requests: {storage: "$size"}}
EOF
  kubectl -n "$ns" wait pvc/"$pvc" --for=jsonpath='{.status.phase}'=Bound --timeout=60s >/dev/null
  echo "rebound: $ns/$pvc -> $newpv ($ENC_CLASS)"

  # --- 6. backup group label -----------------------------------------
  kubectl -n longhorn-system label volumes.longhorn.io "$newpv" recurring-job-group.longhorn.io/backup=enabled --overwrite >/dev/null
  echo "longhorn volume $newpv labeled into group backup"

  echo
  echo "NEXT: commit the manifest for $ns/$pvc with storageClassName: $ENC_CLASS (or no class),"
  echo "      push, then: $0 finish $ns $pvc"
  [ ${#wl[@]} -gt 0 ] && echo "      (workloads at 0 until then: ${wl[*]})"
  ;;

finish)
  app=$(app_of)
  [ -n "$app" ] || { echo "no argocd tracking-id on $ns/$pvc"; exit 1; }
  resume_app "$app"
  sleep 15
  kubectl -n argocd get app "$app" -o jsonpath='{.status.sync.status} {.status.health.status}{"\n"}'
  ;;
*)
  echo "unknown mode $mode"; exit 2;;
esac
