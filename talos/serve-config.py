#!/usr/bin/env python3
"""Talos machine config server.

Scans machines/<mac>/ directories for meta.yaml (ip, config, patches)
and optional patch.yaml, then serves composed configs via
talosctl machineconfig patch.
"""

import http.server
import subprocess
import tempfile
import urllib.parse
from pathlib import Path

import yaml

ROOT = Path(subprocess.run(
    ["git", "rev-parse", "--show-toplevel"],
    capture_output=True, text=True, check=True
).stdout.strip()) / "talos"
MACHINES_DIR = ROOT / "machines"


def load_machines():
    """Scan machines/ for directories, return MAC → meta dict."""
    machines = {}
    for d in MACHINES_DIR.iterdir():
        if not d.is_dir():
            continue
        meta_file = d / "meta.yaml"
        if not meta_file.exists():
            continue
        mac = d.name.replace("-", ":")
        with open(meta_file) as f:
            meta = yaml.safe_load(f)
        meta["_dir"] = str(d)
        machines[mac] = meta
    return machines


def build_config(meta):
    """Compose base config + patches using talosctl machineconfig patch."""
    config_path = ROOT / meta["config"]
    patch_paths = [ROOT / p for p in meta.get("patches", [])]

    # Include machine-specific patch if it exists
    machine_patch = Path(meta["_dir"]) / "patch.yaml"
    if machine_patch.exists():
        patch_paths.append(machine_patch)

    cmd = ["talosctl", "machineconfig", "patch", str(config_path)]
    for patch in patch_paths:
        cmd.extend(["--patch", f"@{patch}"])

    with tempfile.NamedTemporaryFile(suffix=".yaml", delete=False) as tmp:
        tmp_path = tmp.name

    cmd.extend(["-o", tmp_path])

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        Path(tmp_path).unlink(missing_ok=True)
        raise RuntimeError(f"talosctl machineconfig patch failed: {result.stderr}")

    body = Path(tmp_path).read_text()
    Path(tmp_path).unlink(missing_ok=True)
    return body


class ConfigHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        params = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)

        mac = params.get("mac", [None])[0]
        if not mac:
            self.send_error(400, "Missing mac parameter")
            return

        mac = mac.lower().replace("-", ":")
        machines = load_machines()

        if mac not in machines:
            self.send_error(404, f"No config for MAC {mac}")
            return

        try:
            body = build_config(machines[mac]).encode()
        except RuntimeError as e:
            self.send_error(500, str(e))
            return

        self.send_response(200)
        self.send_header("Content-Type", "application/x-yaml")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(description="Serve Talos machine configs")
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--bind", default="0.0.0.0")
    args = parser.parse_args()

    machines = load_machines()
    server = http.server.HTTPServer((args.bind, args.port), ConfigHandler)
    print(f"Serving configs on {args.bind}:{args.port}")
    for mac, meta in machines.items():
        print(f"  {mac} -> {meta['config']}")
    server.serve_forever()
