#!/usr/bin/env python3
"""Extract secrets from Talos machine configs into strategic merge patches.

Reads a machine config, extracts secret fields into a separate YAML file
(a valid Talos strategic merge patch), and strips the secret values from
the original config (replacing with "") while preserving all comments.
"""

import yaml
import sys
import copy
from pathlib import Path

# Dot-separated paths to secret fields in a Talos machine config
SECRET_FIELDS = [
    "machine.token",
    "machine.ca.crt",
    "machine.ca.key",
    "cluster.id",
    "cluster.secret",
    "cluster.token",
    "cluster.secretboxEncryptionSecret",
    "cluster.ca.crt",
    "cluster.ca.key",
    "cluster.aggregatorCA.crt",
    "cluster.aggregatorCA.key",
    "cluster.serviceAccount.key",
    "cluster.etcd.ca.crt",
    "cluster.etcd.ca.key",
]


def get_nested(d, path):
    for key in path.split("."):
        if not isinstance(d, dict) or key not in d:
            return None
        d = d[key]
    return d


def set_nested(d, path, value):
    keys = path.split(".")
    for key in keys[:-1]:
        d = d.setdefault(key, {})
    d[keys[-1]] = value


def extract(config_path, secrets_path):
    text = Path(config_path).read_text()
    config = yaml.safe_load(text)

    if config is None:
        print(f"Skipping {config_path}: empty or invalid YAML")
        return

    secrets = {}
    found = 0

    for field in SECRET_FIELDS:
        value = get_nested(config, field)
        if value is not None:
            set_nested(secrets, field, value)
            # Replace the value in the original text, preserving structure
            if isinstance(value, str):
                text = text.replace(value, '""', 1)
            found += 1

    if found == 0:
        print(f"Skipping {config_path}: no secrets found")
        return

    Path(secrets_path).parent.mkdir(parents=True, exist_ok=True)
    with open(secrets_path, "w") as f:
        yaml.dump(secrets, f, default_flow_style=False, width=10000)

    with open(config_path, "w") as f:
        f.write(text)

    print(f"Extracted {found} secret fields: {config_path} -> {secrets_path}")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print(f"Usage: {sys.argv[0]} <config.yaml> <secrets.yaml>")
        sys.exit(1)
    extract(sys.argv[1], sys.argv[2])
