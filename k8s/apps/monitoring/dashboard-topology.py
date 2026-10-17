#!/usr/bin/env python3
"""Generate dashboard-topology.yaml (the Cluster / Topology Grafana dashboard).

Run from this directory: python3 dashboard-topology.py
"""
import json
import os

HEADER = """\
# Grafana "Cluster / Topology": the Nomad topology view for this
# cluster. One row per node (repeated over $node); per node the running
# pod count, role, allocatable memory/CPU, and two stacked bars — Memory
# and CPU — where each segment is one pod's requests / node allocatable.
# Hover a segment for the pod. $highlight (namespaces, multi) paints the
# matching pods blue, the rest green, Pending pods yellow: Nomad's
# "where do this job's allocations live" without the lines. Pods that
# declare no requests are invisible in the bars by construction; that
# is a fact about the pod, not the panel.
#
# The sidecar (kube-prometheus-stack.yaml) picks up any ConfigMap
# labelled grafana_dashboard=1 in any namespace and files it under the
# grafana_folder annotation; this is the first dashboard in git and
# establishes the convention the chart comment promised.
#
# The JSON is generated — edit dashboard-topology.py next to this file
# and re-run `python3 dashboard-topology.py` (that is what regenerates
# this whole YAML); the Grafana UI is for trying things, not for saving
# (the provider has allowUiUpdates=false, so edits there do not stick).
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-dashboard-topology
  namespace: monitoring
  labels:
    grafana_dashboard: "1"
  annotations:
    grafana_folder: Cluster
data:
  topology.json: |
"""

DS = {"type": "prometheus", "uid": "${datasource}"}

RUNNING = 'max by (namespace,pod) (kube_pod_status_phase{phase="Running"} == 1)'
PENDING = 'max by (namespace,pod) (kube_pod_status_phase{phase="Pending"} == 1)'
ALLOC = 'max by (node,resource) (kube_node_status_allocatable{resource=~"cpu|memory"})'


def ratio(node_sel, ns_sel, phase):
    """requests / allocatable per (node, namespace, pod, resource)."""
    return (
        f'sum by (node,namespace,pod,resource) (kube_pod_container_resource_requests{{resource=~"cpu|memory",{node_sel}{ns_sel}}})'
        f' * on (namespace,pod) group_left() {phase}'
        f' / on (node,resource) group_left() {ALLOC}'
    )


def tagged(expr, prefix):
    """Add alloc="<prefix>namespace/pod" and res="Memory"|"CPU" labels."""
    e = f'label_join({expr}, "alloc", "/", "namespace", "pod")'
    if prefix:
        e = f'label_replace({e}, "alloc", "{prefix}$1", "alloc", "(.*)")'
    e = f'label_replace({e}, "res", "Memory", "resource", "memory")'
    e = f'label_replace({e}, "res", "CPU", "resource", "cpu")'
    return e


NODE = 'node="$node"'
BARS_EXPR = "\n or\n".join([
    tagged(ratio(NODE, ',namespace!~"$highlight"', RUNNING), ""),
    tagged(ratio(NODE, ',namespace=~"$highlight"', RUNNING), "★ "),
    tagged(ratio(NODE, "", PENDING), "⏳ "),
])


def stat(title, expr, unit, x, y, w, color="text", decimals=None, links=None, h=3):
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
        "fieldConfig": {"defaults": {"unit": unit, "color": {"mode": "fixed", "fixedColor": color}}, "overrides": []},
    }
    if decimals is not None:
        p["fieldConfig"]["defaults"]["decimals"] = decimals
    if links:
        p["links"] = links
    return p


def bars(y):
    return {
        "type": "barchart",
        "title": "Reserved (requests / allocatable) — one segment per pod",
        "datasource": DS,
        "gridPos": {"x": 6, "y": y, "w": 18, "h": 6},
        "targets": [{"refId": "A", "datasource": DS, "expr": BARS_EXPR, "instant": True, "range": False, "format": "table"}],
        "transformations": [
            {"id": "groupingToMatrix", "options": {"columnField": "alloc", "rowField": "res", "valueField": "Value", "emptyValue": "zero"}},
            {"id": "sortBy", "options": {"sort": [{"field": "res\\alloc", "desc": True}]}},
        ],
        "options": {
            "orientation": "horizontal", "stacking": "normal", "xField": "res\\alloc",
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
                {"matcher": {"id": "byRegexp", "options": "^⏳ .*"},
                 "properties": [{"id": "color", "value": {"mode": "fixed", "fixedColor": "yellow"}}]},
            ],
        },
    }


def node_running_pods(sel):
    return f'count(kube_pod_info{{{sel}}} * on (namespace,pod) group_left() {RUNNING})'


def reserved_fraction(sel, resource):
    return (
        f'sum(sum by (namespace,pod) (kube_pod_container_resource_requests{{resource="{resource}",{sel}}})'
        f' * on (namespace,pod) group_left() {RUNNING})'
        f' / sum(kube_node_status_allocatable{{resource="{resource}",{sel}}})'
    )


node_pods_link = [{
    "title": "Compute Resources / Node (Pods)",
    "url": "/d/200ac8fdbfbb74b39aff88118e4d1c2c/?var-node=${node}&${__url_time_range}",
}]

panels = []
# --- Cluster header -------------------------------------------------------
panels.append({"type": "row", "title": "Cluster", "collapsed": False, "gridPos": {"x": 0, "y": 0, "w": 24, "h": 1}, "panels": []})
panels += [
    stat("Nodes", "count(kube_node_info)", "none", 0, 1, 3),
    stat("Running pods", node_running_pods('node!=""'), "none", 3, 1, 3),
    stat("Memory reserved", reserved_fraction('node!=""', "memory"), "percentunit", 6, 1, 3, decimals=0),
    stat("CPU reserved", reserved_fraction('node!=""', "cpu"), "percentunit", 9, 1, 3, decimals=0),
    stat("Memory allocatable", 'sum(kube_node_status_allocatable{resource="memory"})', "bytes", 12, 1, 3),
    stat("CPU allocatable", 'sum(kube_node_status_allocatable{resource="cpu"})', "suffix: cores", 15, 1, 3, decimals=1),
    {
        "type": "text", "title": "Legend",
        "gridPos": {"x": 18, "y": 1, "w": 6, "h": 3},
        "options": {"mode": "markdown", "content":
            "🟩 running &nbsp; 🟦 in **$highlight** &nbsp; 🟨 pending  \n"
            "Segments: pod `requests / allocatable`"},
    },
]
# --- Per-node repeated row --------------------------------------------------
Y = 4
panels.append({"type": "row", "title": "$node", "repeat": "node", "collapsed": False,
               "gridPos": {"x": 0, "y": Y, "w": 24, "h": 1}, "panels": []})
panels += [
    stat("Running pods", node_running_pods(NODE), "none", 0, Y + 1, 3, links=node_pods_link),
    # kube_node_role exists only for labelled roles (control-plane); an
    # unlabelled node is a worker.
    stat("Role", f'max by (role) (kube_node_role{{{NODE}}}) or label_replace(absent(kube_node_role{{{NODE}}}), "role", "worker", "", "")', "none", 3, Y + 1, 3),
    stat("Memory allocatable", f'max(kube_node_status_allocatable{{resource="memory",{NODE}}})', "bytes", 0, Y + 4, 3),
    stat("CPU allocatable", f'max(kube_node_status_allocatable{{resource="cpu",{NODE}}})', "suffix: cores", 3, Y + 4, 3, decimals=2),
    bars(Y + 1),
]
# Role stat: show the label, not the value 1.
panels[-4]["options"]["textMode"] = "name"
panels[-4]["targets"][0]["legendFormat"] = "{{role}}"

dashboard = {
    "uid": "cluster-topology",
    "title": "Cluster / Topology",
    "tags": ["cluster", "topology"],
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
         "query": {"query": "label_values(kube_node_info, node)", "refId": "nodes"},
         "definition": "label_values(kube_node_info, node)",
         "refresh": 2, "sort": 1, "multi": True, "includeAll": True,
         "current": {"text": ["All"], "value": ["$__all"]}},
        {"name": "highlight", "type": "query", "label": "Highlight namespace", "datasource": DS,
         "query": {"query": "label_values(kube_pod_info, namespace)", "refId": "ns"},
         "definition": "label_values(kube_pod_info, namespace)",
         "refresh": 2, "sort": 1, "multi": True, "includeAll": True, "allValue": "__none__",
         "current": {"text": ["All"], "value": ["$__all"]}},
    ]},
    "links": [
        {"title": "Nodes Overview", "type": "link", "url": "/d/058020e04168bfdea0c52269cb699df2/", "icon": "dashboard"},
        {"title": "Compute Resources / Cluster", "type": "link", "url": "/d/efa86fd1d0c121a26444b636a3f509a8/", "icon": "dashboard"},
    ],
    "panels": panels,
}

for i, p in enumerate(dashboard["panels"], start=1):
    p["id"] = i

body = json.dumps(dashboard, indent=2, ensure_ascii=False)
out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "dashboard-topology.yaml")
with open(out, "w") as f:
    f.write(HEADER + "".join("    " + l + "\n" for l in body.splitlines()))
print("wrote", out)
