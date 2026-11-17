#!/usr/bin/env bash
# Move w2's RTX 3070 between its two owners (epic talos-config-e6rx,
# spike dsuj's "VM <-> AI-pod hand-off"):
#
#   vm    vfio-pci holds it  -> nvidia.com/GA104_GEFORCE_RTX_3070: 1
#                               (KubeVirt passthrough, win2k25)
#   host  nvidia holds it    -> nvidia.com/gpu: 1  (pods, ComfyUI)
#
# The card is on exactly one driver at a time. The boot default is `vm`
# (vfio_pci ids= in talos/hardware/asus-tuf-b550-pro.yaml claims it
# before the nvidia modules load), so a reboot always hands it back.
#
#   scripts/gpu-handoff.sh status
#   scripts/gpu-handoff.sh host     # refuses while a VMI holds the card
#   scripts/gpu-handoff.sh vm       # refuses while a pod holds nvidia.com/gpu
#
# UNTESTED past `status` as of 2026-11-17: w2 does not run the -nvidia
# installer yet (its reboot bounces win2k25 — waiting on the owner).
# Read it as the design until this line is removed.
#
# It never stops the VM or a pod for you — losing a Windows session or
# a render is the operator's call (vmpower / `kubectl scale`).
#
# Talos has no shell, so the three sysfs writes run in a one-shot
# privileged pod pinned to the node. Only the VGA function moves; its
# HDA function (0b:00.1) stays on vfio-pci throughout — nothing on the
# host wants it, and the VM needs both from the same IOMMU group.
set -euo pipefail
NODE=${GPU_NODE:-w2}
NODE_IP=${GPU_NODE_IP:-10.0.0.77}
ADDR=${GPU_ADDR:-0000:0b:00.0}
VFIO_RES='nvidia.com/GA104_GEFORCE_RTX_3070'
BUSYBOX=busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec

driver() { talosctl -n "$NODE_IP" read "/sys/bus/pci/devices/$ADDR/uevent" | sed -n 's/^DRIVER=//p'; }
alloc() { kubectl get node "$NODE" -o json | jq -r --arg r "$1" '.status.allocatable[$r] // "0"'; }
status() {
  echo "$NODE $ADDR driver: $(driver || echo none)"
  echo "  $VFIO_RES: $(alloc "$VFIO_RES")   nvidia.com/gpu: $(alloc nvidia.com/gpu)"
}

# rebind <to-driver>: unbind from whatever holds the card, pin the next
# probe to <to-driver> with driver_override, probe.
rebind() {
  local to=$1 name=gpu-handoff-$RANDOM
  kubectl -n kube-system run "$name" --restart=Never --image="$BUSYBOX" --overrides="$(jq -n \
    --arg node "$NODE" --arg addr "$ADDR" --arg to "$to" --arg img "$BUSYBOX" --arg name "$name" '{
    spec: {nodeName: $node, restartPolicy: "Never", tolerations: [{operator: "Exists"}],
      containers: [{name: $name, image: $img, securityContext: {privileged: true},
        command: ["sh", "-ec", "
          d=/sys/bus/pci/devices/\($addr)
          [ -e $d/driver ] && echo \($addr) > $d/driver/unbind
          echo \($to) > $d/driver_override
          echo \($addr) > /sys/bus/pci/drivers_probe
          echo bound: $(basename $(readlink $d/driver))"],
        volumeMounts: [{name: sys, mountPath: "/sys"}]}],
      volumes: [{name: sys, hostPath: {path: "/sys"}}]}}')" >/dev/null
  kubectl -n kube-system wait --for=jsonpath='{.status.phase}'=Succeeded "pod/$name" --timeout=90s >/dev/null ||
    { kubectl -n kube-system logs "$name" >&2 || true; kubectl -n kube-system delete pod "$name" --wait=false >/dev/null; return 1; }
  kubectl -n kube-system logs "$name"
  kubectl -n kube-system delete pod "$name" --wait=false >/dev/null
}

# The device plugins enumerate at start and do not watch for a rebind.
bounce() { kubectl -n "$1" delete pod -l "$2" --field-selector "spec.nodeName=$NODE" --wait=false >/dev/null; }
wait_alloc() {
  for _ in $(seq 1 60); do [ "$(alloc "$1")" = "$2" ] && return 0; sleep 2; done
  echo "timed out waiting for $1=$2 on $NODE" >&2; return 1
}

case ${1:-status} in
  status) status ;;
  host)
    [ "$(driver)" = nvidia ] && { echo "already on the host"; status; exit 0; }
    if kubectl get vmi -A -o json | jq -e --arg n "$NODE" --arg r "$VFIO_RES" \
      '[.items[] | select(.status.nodeName == $n) | select([.spec.domain.devices.hostDevices[]?.deviceName] | index($r))] | length > 0' >/dev/null; then
      echo "REFUSED: a VMI on $NODE holds $VFIO_RES — stop the VM first (vmpower)." >&2; exit 1
    fi
    # persistenced holds the device open once nvidia has it; it is
    # stopped here only so a previous half-finished hand-off cannot
    # block the bind, and started again below.
    talosctl -n "$NODE_IP" service ext-nvidia-persistenced stop >/dev/null 2>&1 || true
    rebind nvidia
    talosctl -n "$NODE_IP" service ext-nvidia-persistenced start >/dev/null 2>&1 || true
    bounce kube-system name=nvidia-device-plugin
    bounce kubevirt kubevirt.io=virt-handler
    wait_alloc nvidia.com/gpu 1
    status ;;
  vm)
    [ "$(driver)" = vfio-pci ] && { echo "already with the VM"; status; exit 0; }
    if kubectl get pods -A -o json | jq -e --arg n "$NODE" \
      '[.items[] | select(.spec.nodeName == $n and (.status.phase == "Running" or .status.phase == "Pending"))
        | select([.spec.containers[].resources.limits["nvidia.com/gpu"] // empty] | length > 0)] | length > 0' >/dev/null; then
      echo "REFUSED: a pod on $NODE holds nvidia.com/gpu — scale it to 0 first." >&2; exit 1
    fi
    # nvidia's remove() waits for every client; persistenced is one.
    talosctl -n "$NODE_IP" service ext-nvidia-persistenced stop >/dev/null 2>&1 || true
    rebind vfio-pci
    bounce kube-system name=nvidia-device-plugin
    bounce kubevirt kubevirt.io=virt-handler
    wait_alloc "$VFIO_RES" 1
    status ;;
  *) echo "usage: $0 status|host|vm" >&2; exit 2 ;;
esac
