#!/usr/bin/env bash
# Run the backup restore test (k8s/apps/backup-verify) now, for one
# volume or all of them, and wait for the verdict.
#
#   scripts/verify-backup.sh                    # every volume in the backup group
#   scripts/verify-backup.sh files/transfer     # one claim (<ns>/<pvc>) or Longhorn volume name
#   scripts/verify-backup.sh files/transfer --manifest   # also save the sorted sha256 manifest
#
# The Job is created from the CronJob's own template (same image, SA,
# script), with VOLUME / MANIFEST overridden, so this tests exactly what
# Sunday's run tests. A successful run counts for BackupVerifyStale.
# With --manifest the restored volume's manifest lands in
# /tmp/backup-verify-<job>.manifest — diff it against the same command
# run over the live volume (ADR-0044 did this for transfer).
#
# Restores pull from Tigris at ~12 MB/s: seconds for the small volumes,
# ~30 min for transfer, ~1 h for everything. Waits up to 3 h; Ctrl-C
# leaves the Job running (it cleans up after itself).
set -euo pipefail

volume=""; manifest=0
for a in "$@"; do
  case "$a" in
    --manifest) manifest=1 ;;
    -h|--help) sed -n 2,19p "$0"; exit 0 ;;
    *) volume=$a ;;
  esac
done

slug=$(echo "${volume:-all}" | tr '/' '-' | tr -cd 'a-z0-9-' | cut -c1-40)
job="verify-$slug-$(date +%H%M%S)"

kubectl -n backup-verify create job --from=cronjob/backup-verify "$job" --dry-run=client -o json \
  | jq --arg v "$volume" --arg m "$manifest" \
      '.spec.template.spec.containers[0].env = [
         {"name":"MAX_AGE_H","value":"30"},
         {"name":"VOLUME","value":$v},
         {"name":"MANIFEST","value":$m}]' \
  | kubectl apply -f - 2>&1 | grep -v '^Warning' || true
echo "job backup-verify/$job started (${volume:-all volumes}); waiting..."

deadline=$(( $(date +%s) + 3*3600 ))
while :; do
  s=$(kubectl -n backup-verify get job "$job" -o jsonpath='{.status.succeeded}{"/"}{.status.failed}')
  [ "$s" = "1/" ] && verdict=ok && break
  [ "${s#*/}" = 1 ] && verdict=fail && break
  [ "$(date +%s)" -gt "$deadline" ] && { echo "still running after 3 h; check: kubectl -n backup-verify logs job/$job"; exit 2; }
  sleep 15
done

kubectl -n backup-verify logs "job/$job" | grep -v '^M ' | grep -v '^Warning'
if [ "$manifest" = 1 ]; then
  out="/tmp/backup-verify-$job.manifest"
  kubectl -n backup-verify logs "job/$job" | grep '^M ' | sort > "$out"
  echo "manifest: $out ($(wc -l < "$out") files)"
fi
[ "$verdict" = ok ]
