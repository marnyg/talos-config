#!/usr/bin/env python3
"""Generate dashboard-storage-topology.yaml (the Storage / Topology Grafana dashboard).

Run from this directory: python3 dashboard-storage-topology.py
Sibling of dashboard-topology.py (pods); same panel grammar, Longhorn data.
"""
import json
import os

HEADER = """\
# Grafana "Storage / Topology": the pod topology dashboard's twin for
# disk. One row per node (repeated over $node); per node the Longhorn
# replica count, capacity, scheduled/used fractions, attached volumes,
# the /var filesystem (images, emptyDirs, ephemeral) — and two bar
# panels, one bar per Longhorn disk on the node (nas1 has three: nvme +
# bulk-1 + bulk-2), each segment one replica labelled namespace/pvc:
#   Scheduled  volume size        / disk capacity  (what Longhorn booked)
#   Used       volume actual size / disk capacity  (what is on the disk)
# $highlight (pvc namespaces) paints matching replicas blue; stopped
# replicas (a detached volume — normal) grey; rebuilding/error yellow.
#
# Replica placement is longhorn_replica_info{node,disk_path,volume};
# sizes join on `volume` (longhorn_volume_*_bytes carry the ATTACHED
# node, which is dropped — replicas live elsewhere). Disk capacity is
# longhorn_disk_capacity_bytes{disk}, keyed by disk NAME, while
# replica_info carries the disk PATH; the metrics never state the
# mapping, so DISK_CAP below rewrites the name into the path by this
# cluster's convention (nodes.longhorn.io spec.disks):
#   default-disk-*  -> /var/mnt/longhorn      (longhorn/application.yaml)
#   bulk-N          -> /var/mnt/longhorn-N    (nas1)
# A disk named outside that convention shows up with no bar.
#
# Generated: edit dashboard-storage-topology.py next to this file and
# re-run it; UI edits do not stick (provider allowUiUpdates=false).
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-dashboard-storage-topology
  namespace: monitoring
  labels:
    grafana_dashboard: "1"
  annotations:
    grafana_folder: Cluster
data:
  storage-topology.json: |
"""

DS = {"type": "prometheus", "uid": "${datasource}"}
NODE = 'node="$node"'

# Longhorn disk capacity keyed by disk PATH (see HEADER).
DISK_CAP = (
    'max by (node,disk_path) (label_replace(label_replace('
    'longhorn_disk_capacity_bytes{node="$node"}, '
    '"disk_path", "/var/mnt/longhorn", "disk", "default-disk-.*"), '
    '"disk_path", "/var/mnt/longhorn-$1", "disk", "bulk-(.*)"))'
)
REPLICAS = 'max by (node,disk_path,replica,volume) (longhorn_replica_info{node="$node"})'


def replica_state(sel):
    return f'max by (node,replica) (longhorn_replica_state{{node="$node",{sel}}} == 1)'


def volsize(metric, ns_sel):
    return f'max by (volume,pvc,pvc_namespace) ({metric}{{{ns_sel}}})'


def segments(metric, ns_sel, state_sel, prefix):
    """Per-replica fraction of its disk, labelled alloc="<prefix>ns/pvc"."""
    e = (
        f'sum by (node,disk_path,volume,pvc,pvc_namespace) ('
        f'({REPLICAS} * on (node,replica) group_left() {replica_state(state_sel)})'
        f' * on (volume) group_left(pvc,pvc_namespace) {volsize(metric, ns_sel)})'
        f' / on (node,disk_path) group_left() {DISK_CAP}'
    )
    e = f'label_join({e}, "alloc", "/", "pvc_namespace", "pvc")'
    if prefix:
        e = f'label_replace({e}, "alloc", "{prefix}$1", "alloc", "(.*)")'
    return e


def bars_expr(metric):
    return "\n or\n".join([
        segments(metric, 'pvc_namespace!~"$highlight"', 'state="running"', ""),
        segments(metric, 'pvc_namespace=~"$highlight"', 'state="running"', "★ "),
        segments(metric, "", 'state="stopped"', "⏸ "),
        segments(metric, "", 'state!~"running|stopped"', "⏳ "),
    ])


def stat(title, expr, unit, x, y, w, decimals=None, links=None, h=3, thresholds=None):
    p = {
        "type": "stat",
        "title": title,
        "datasource": DS,
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": [{"refId": "A", "datasource": DS, "expr": expr, "instant": True, "range": False}],
        "options": {
            "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
            "colorMode": "none", "graphMode": "none", "textMode": "value",
            "justifyMode": "center", "orientation": "auto", "wideLayout": True,
        },
        "fieldConfig": {"defaults": {"unit": unit, "color": {"mode": "fixed", "fixedColor": "text"}}, "overrides": []},
    }
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    if thresholds:
        p["options"]["colorMode"] = "value"
        p["fieldConfig"]["defaults"]["color"] = {"mode": "thresholds"}
        p["fieldConfig"]["defaults"]["thresholds"] = {"mode": "absolute", "steps": thresholds}
    if links:
        p["links"] = links
    return p


def bars(title, metric, x, y, w, h):
    return {
        "type": "barchart",
        "title": title,
        "datasource": DS,
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": [{"refId": "A", "datasource": DS, "expr": bars_expr(metric), "instant": True, "range": False, "format": "table"}],
        "transformations": [
            {"id": "groupingToMatrix", "options": {"columnField": "alloc", "rowField": "disk_path", "valueField": "Value", "emptyValue": "zero"}},
            {"id": "sortBy", "options": {"sort": [{"field": "disk_path\\alloc", "desc": False}]}},
        ],
        "options": {
            "orientation": "horizontal", "stacking": "normal", "xField": "disk_path\\alloc",
            "showValue": "never", "barWidth": 0.85, "groupWidth": 0.7, "barRadius": 0,
            "fullHighlight": False, "xTickLabelRotation": 0, "xTickLabelSpacing": 0,
            "legend": {"showLegend": False, "displayMode": "list", "placement": "bottom", "calcs": []},
            "tooltip": {"mode": "single", "sort": "none"},
        },
        "fieldConfig": {
            "defaults": {
                "unit": "percentunit", "min": 0, "max": 1, "decimals": 0,
                "color": {"mode": "fixed", "fixedColor": "green"},
                "custom": {"lineWidth": 2, "fillOpacity": 60, "gradientMode": "none",
                           "axisPlacement": "auto", "axisLabel": "", "axisBorderShow": False,
                           "hideFrom": {"legend": False, "tooltip": False, "viz": False}},
            },
            "overrides": [
                {"matcher": {"id": "byRegexp", "options": "^★ .*"},
                 "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "blue"}}]},
                {"matcher": {"id": "byRegexp", "options": "^⏸ .*"},
                 "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "#888"}}]},
                {"matcher": {"id": "byRegexp", "options": "^⏳ .*"},
                 "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "yellow"}}]},
            ],
        },
    }


def frac(num_metric, sel):
    return f'sum({num_metric}{{{sel}}}) / sum(longhorn_node_storage_capacity_bytes{{{sel}}})'


UNAME = 'max by (instance,nodename) (node_uname_info{nodename="$node"})'
VAR_USED = (
    f'1 - sum(node_filesystem_avail_bytes{{mountpoint="/var"}} * on (instance) group_left(nodename) {UNAME})'
    f' / sum(node_filesystem_size_bytes{{mountpoint="/var"}} * on (instance) group_left(nodename) {UNAME})'
)
UNHEALTHY = 'count(longhorn_volume_robustness{state=~"degraded|faulted"} == 1) or vector(0)'
longhorn_link = [{"title": "Longhorn UI — node", "url": "https://longhorn.gw.mesh.internal/#/node", "targetBlank": True}]
pct_warn = [{"color": "text", "value": None}, {"color": "orange", "value": 0.8}, {"color": "red", "value": 0.9}]
count_warn = [{"color": "text", "value": None}, {"color": "red", "value": 1}]

panels = []
# --- Cluster header -------------------------------------------------------
panels.append({"type": "row", "title": "Cluster", "collapsed": False, "gridPos": {"x": 0, "y": 0, "w": 24, "h": 1}, "panels": []})
panels += [
    stat("Volumes", "count(longhorn_volume_capacity_bytes)", "none", 0, 1, 3),
    stat("Replicas", "count(longhorn_replica_info)", "none", 3, 1, 3),
    stat("Scheduled", frac("longhorn_node_storage_scheduled_bytes", 'node!=""'), "percentunit", 6, 1, 3, decimals=0, thresholds=pct_warn),
    stat("Used", frac("longhorn_node_storage_usage_bytes", 'node!=""'), "percentunit", 9, 1, 3, decimals=0, thresholds=pct_warn),
    stat("Longhorn capacity", "sum(longhorn_node_storage_capacity_bytes)", "bytes", 12, 1, 3),
    stat("Degraded / faulted", UNHEALTHY, "none", 15, 1, 3, thresholds=count_warn),
    {
        "type": "text", "title": "Legend",
        "gridPos": {"x": 18, "y": 1, "w": 6, "h": 3},
        "options": {"mode": "markdown", "content":
            "🟩 running &nbsp; 🟦 in **$highlight** &nbsp; ⬜ stopped &nbsp; 🟨 rebuilding/error  \n"
            "Segments: replica `size / disk capacity`"},
    },
]
# --- Per-node repeated row --------------------------------------------------
Y = 4
panels.append({"type": "row", "title": "$node", "repeat": "node", "collapsed": False,
               "gridPos": {"x": 0, "y": Y, "w": 24, "h": 1}, "panels": []})
panels += [
    stat("Replicas", f'count(longhorn_replica_info{{{NODE}}})', "none", 0, Y + 1, 3, links=longhorn_link),
    stat("Longhorn capacity", f'sum(longhorn_node_storage_capacity_bytes{{{NODE}}})', "bytes", 3, Y + 1, 3),
    stat("Scheduled", frac("longhorn_node_storage_scheduled_bytes", NODE), "percentunit", 0, Y + 4, 3, decimals=0, thresholds=pct_warn),
    stat("Used", frac("longhorn_node_storage_usage_bytes", NODE), "percentunit", 3, Y + 4, 3, decimals=0, thresholds=pct_warn),
    stat("Volumes attached", f'count(longhorn_volume_state{{{NODE},state="attached"}} == 1) or vector(0)', "none", 0, Y + 7, 3),
    stat("/var used", VAR_USED, "percentunit", 3, Y + 7, 3, decimals=0, thresholds=pct_warn),
    bars("Scheduled — replica volume size / disk capacity", "longhorn_volume_capacity_bytes", 6, Y + 1, 9, 9),
    bars("Used — replica actual size / disk capacity", "longhorn_volume_actual_size_bytes", 15, Y + 1, 9, 9),
]

dashboard = {
    "uid": "storage-topology",
    "title": "Storage / Topology",
    "tags": ["cluster", "topology", "longhorn"],
    "timezone": "browser",
    "editable": True,
    "graphTooltip": 0,
    "refresh": "30s",
    "schemaVersion": 39,
    "time": {"from": "now-1h", "to": "now"},
    "templating": {"list": [
        {"name": "datasource", "type": "datasource", "query": "prometheus", "label": "Data source",
         "current": {"text": "Prometheus", "value": "prometheus"}, "hide": 0},
        {"name": "node", "type": "query", "label": "Node", "datasource": DS,
         "query": {"query": "label_values(longhorn_node_storage_capacity_bytes, node)", "refId": "nodes"},
         "definition": "label_values(longhorn_node_storage_capacity_bytes, node)",
         "refresh": 2, "sort": 1, "multi": True, "includeAll": True,
         "current": {"text": ["All"], "value": ["$__all"]}},
        {"name": "highlight", "type": "query", "label": "Highlight namespace", "datasource": DS,
         "query": {"query": "label_values(longhorn_volume_capacity_bytes, pvc_namespace)", "refId": "ns"},
         "definition": "label_values(longhorn_volume_capacity_bytes, pvc_namespace)",
         "refresh": 2, "sort": 1, "multi": True, "includeAll": True, "allValue": "__none__",
         "current": {"text": ["All"], "value": ["$__all"]}},
    ]},
    "links": [
        {"title": "Cluster / Topology (pods)", "type": "link", "url": "/d/cluster-topology/", "icon": "dashboard"},
        {"title": "Longhorn UI", "type": "link", "url": "https://longhorn.gw.mesh.internal/", "icon": "external link", "targetBlank": True},
    ],
    "panels": panels,
}

for i, p in enumerate(dashboard["panels"], start=1):
    p["id"] = i

body = json.dumps(dashboard, indent=2, ensure_ascii=False)
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dashboard-storage-topology.yaml")
with open(out, "w") as f:
    f.write(HEADER + "".join("    " + l + "\n" for l in body.splitlines()))
print("wrote", out)
