#!/usr/bin/env nix-shell
#!nix-shell -i bash -p kubevirt kubectl python3Packages.vncdotool
#
# Reinstall the win2k25 guest from the ISO, hands-free.
#
# DESTROYS the Windows system disk (the ISO and the answer file are
# untouched). The flow:
#   1. delete the system DataVolume + running VMI — virt-controller
#      recreates both from the VM spec (runStrategy Always), giving a
#      blank disk that boots the install ISO
#   2. the EFI ISO shows "Press any key to boot from CD or DVD…" for a
#      few seconds; nothing inside the guest exists yet to answer it,
#      so we answer from outside: proxy the VNC subresource and spam
#      Enter through the window (the one non-declarative step — a
#      no-prompt ISO repack would remove it, deliberately not built)
#   3. autounattend.xml (sysprep CD, sealed secret) does everything
#      else: partitioning, install, admin password, RDP, virtio tools
#
# ~20-30 min after this script exits, RDP is up:
#   xfreerdp3 /v:cp1.mesh.internal:30389 /u:Administrator
set -euo pipefail

NS=vms VM=win2k25 PORT=5901
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export KUBECONFIG="${KUBECONFIG:-$REPO_ROOT/kubeconfig}"

echo ">>> This WIPES the ${VM} system disk and reinstalls Windows."
echo ">>> Ctrl-C within 5s to abort."
sleep 5

echo ">>> Deleting system disk + instance (recreated from VM spec)…"
kubectl delete dv "${VM}-system" -n "$NS" --ignore-not-found --wait=false
kubectl delete vmi "$VM" -n "$NS" --ignore-not-found --wait=false

echo ">>> Waiting for the blank system disk to re-import…"
until [[ "$(kubectl get dv "${VM}-system" -n "$NS" -o jsonpath='{.status.phase}' 2>/dev/null)" == "Succeeded" ]]; do
  sleep 3
done

echo ">>> Waiting for the VM to start…"
until [[ "$(kubectl get vmi "$VM" -n "$NS" -o jsonpath='{.status.phase}' 2>/dev/null)" == "Running" ]]; do
  sleep 3
done

echo ">>> Answering the 'press any key' prompt over VNC…"
virtctl vnc "$VM" -n "$NS" --proxy-only --port "$PORT" >/dev/null 2>&1 &
PROXY=$!
trap 'kill "$PROXY" 2>/dev/null || true' EXIT
# Spam Enter through the boot window. Harmless once setup is past the
# prompt: autounattend has already answered every dialog it could hit.
for _ in $(seq 1 40); do
  vncdo -s "localhost::${PORT}" key enter >/dev/null 2>&1 || true
  sleep 1.5
done

echo ">>> Boot committed. Unattended install running (~20-30 min)."
echo ">>> Watch:  virtctl vnc ${VM} -n ${NS}"
echo ">>> Then:   xfreerdp3 /v:cp1.mesh.internal:30389 /u:Administrator"
