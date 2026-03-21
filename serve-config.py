#!/usr/bin/env python3
"""Talos machine config server.

Maps MAC addresses to config files + patches, merges them using
talosctl machineconfig patch, and serves the result.
"""

import json
import http.server
import subprocess
import tempfile
import urllib.parse
from pathlib import Path

ROOT = Path.cwd()
MACHINES_FILE = ROOT / "machines.json"


def load_machines():
    data = json.loads(MACHINES_FILE.read_text())
    return {k.lower().replace("-", ":"): v for k, v in data.items()}


def apply_patches(config_path, patch_paths):
    """Merge config + patches using talosctl machineconfig patch."""
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
        entry = machines.get(mac)

        if not entry:
            self.send_error(404, f"No config for MAC {mac}")
            return

        config_path = ROOT / entry["config"]
        patch_paths = [ROOT / p for p in entry.get("patches", [])]

        if not config_path.exists():
            self.send_error(500, f"Config file not found: {entry['config']}")
            return

        for p in patch_paths:
            if not p.exists():
                self.send_error(500, f"Patch file not found: {p}")
                return

        try:
            if patch_paths:
                body = apply_patches(config_path, patch_paths).encode()
            else:
                body = config_path.read_bytes()
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

    server = http.server.HTTPServer((args.bind, args.port), ConfigHandler)
    print(f"Serving configs on {args.bind}:{args.port}")
    print(f"Machines: {json.dumps(load_machines(), indent=2)}")
    server.serve_forever()
