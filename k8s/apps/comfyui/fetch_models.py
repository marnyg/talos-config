#!/usr/bin/env python3
"""Fill a ComfyUI model tree from models.txt. Stdlib only.

    fetch_models.py <max-tier> [models-dir]

Why not `hf download` (what this replaced on 2026-11-17): an
interrupted `hf download` did not resume — every pod restart began the
file again under a new temp name, and five restarts left 8 GB of dead
partials of one 5 GB file on w1. Here the WAN is the scarce thing
(~10-15 MB/s for the whole house; a tier is 80 GB), so:

- every download resumes: bytes go to <file>.part with an HTTP Range
  request, and the file is renamed into place only when complete;
- a finished file is checked against the sha256 the hub publishes for
  it (X-Linked-ETag), wherever the bytes came from;
- MODEL_MIRROR=http://peer:8189 names another node's model tree (the
  pod's `models` container serves it). The peer is asked before
  Hugging Face, and a background sweep takes whatever the peer has
  finished every minute. With MODEL_ORDER=reverse on one of two peers
  they start from opposite ends of the manifest and each file crosses
  the WAN about once.

Exit status is non-zero if anything is still missing, so a container
running this restarts and carries on where it stopped.
"""
import fnmatch
import hashlib
import json
import os
import shutil
import sys
import threading
import time
import urllib.error
import urllib.request

HF = "https://huggingface.co"
UA = {"User-Agent": "talos-config-fetch-models"}
here = os.path.dirname(os.path.abspath(__file__))
tier = int(sys.argv[1])
models = sys.argv[2] if len(sys.argv) > 2 else os.environ.get(
    "MODELS", os.path.expanduser("~/disks/1TB-2024/ai/models"))
mirror = os.environ.get("MODEL_MIRROR", "").rstrip("/")
locks, locks_guard = {}, threading.Lock()


def log(msg):
    print(f"== {time.strftime('%T')} {msg}", flush=True)


def get(url, **kw):
    return urllib.request.urlopen(urllib.request.Request(url, headers={**UA, **kw.pop("headers", {})}, **kw),
                                  timeout=60)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


def hub_meta(repo, path):
    """(size, sha256) as the hub states them, read before the CDN redirect."""
    req = urllib.request.Request(f"{HF}/{repo}/resolve/main/{path}", method="HEAD", headers=UA)
    try:
        h = urllib.request.build_opener(NoRedirect).open(req, timeout=30).headers
    except urllib.error.HTTPError as e:
        if e.code not in (301, 302, 303, 307, 308):
            raise
        h = e.headers
    size = h.get("X-Linked-Size") or h.get("Content-Length")
    sha = (h.get("X-Linked-ETag") or "").strip('"')
    return (int(size) if size else None, sha if len(sha) == 64 else None)


def entries():
    out = []
    for line in open(os.path.join(here, "models.txt")):
        f = line.split("#", 1)[0].split()
        if len(f) < 3 or int(f[0]) > tier:
            continue
        repo, path = f[1], f[2]
        if "*" in path:  # a directory glob: ask the hub what is in it
            with get(f"{HF}/api/models/{repo}/tree/main/{os.path.dirname(path)}") as r:
                out += [(repo, e["path"]) for e in json.load(r)
                        if e["type"] == "file" and fnmatch.fnmatch(e["path"], path)]
        else:
            out.append((repo, path))
    return out


def pull(url, part, size):
    """Append to `part` from `url` until it is `size` bytes (or the server's idea of complete)."""
    have = os.path.getsize(part) if os.path.exists(part) else 0
    if size and have > size:
        os.remove(part)
        have = 0
    if size and have == size:
        return
    try:
        r = get(url, headers={"Range": f"bytes={have}-"} if have else {})
    except urllib.error.HTTPError as e:
        if e.code == 416 and have:  # nothing past what we hold
            return
        raise
    with r:
        if have and r.status != 206:  # the server ignored the range: start over
            have = 0
        total = have + int(r.headers["Content-Length"]) if r.headers.get("Content-Length") else size
        with open(part, "ab" if have else "wb") as f:
            shutil.copyfileobj(r, f, 4 << 20)
    got = os.path.getsize(part)
    if total and got != total:
        raise OSError(f"short read {got}/{total}")


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while b := f.read(8 << 20):
            h.update(b)
    return h.hexdigest()


def fetch(repo, path, mirror_only=False, wait=True):
    dst = os.path.join(models, path)
    if os.path.exists(dst):
        return True
    with locks_guard:
        lock = locks.setdefault(path, threading.Lock())
    if not lock.acquire(blocking=wait):
        return False  # the other thread is on this file
    try:
        if os.path.exists(dst):
            return True
        os.makedirs(os.path.dirname(dst), exist_ok=True)
        try:
            size, sha = hub_meta(repo, path)
        except Exception:
            size, sha = None, None  # WAN down: the mirror can still serve, unverified by hash
        sources = ([("mirror", f"{mirror}/{path}")] if mirror else []) + (
            [] if mirror_only else [("hub", f"{HF}/{repo}/resolve/main/{path}")])
        for name, url in sources:
            try:
                pull(url, dst + ".part", size)
            except (urllib.error.URLError, OSError, TimeoutError) as e:
                if name == "hub":
                    log(f"hub failed {path}: {e}")
                continue
            if sha and sha256(dst + ".part") != sha:
                log(f"sha256 mismatch from {name}, discarded: {path}")
                os.remove(dst + ".part")
                continue
            os.replace(dst + ".part", dst)
            log(f"{name:6} {path}  ({os.path.getsize(dst) / 1e9:.1f} GB{'' if sha else ', no hash to check'})")
            return True
        return False
    finally:
        lock.release()


def main():
    os.makedirs(models, exist_ok=True)
    shutil.rmtree(os.path.join(models, ".cache"), ignore_errors=True)  # dead `hf download` partials
    todo = entries()
    if mirror:
        def sweep():
            while True:
                for repo, path in todo:
                    fetch(repo, path, mirror_only=True, wait=False)
                time.sleep(60)
        for repo, path in todo:  # once, synchronously: everything the peer already has
            fetch(repo, path, mirror_only=True)
        threading.Thread(target=sweep, daemon=True).start()
    order = todo[::-1] if os.environ.get("MODEL_ORDER") == "reverse" else todo
    missing = []
    for repo, path in order:
        for attempt in range(6):
            if fetch(repo, path):
                break
            time.sleep(20)
        else:
            missing.append(path)
    total = sum(os.path.getsize(os.path.join(models, p)) for _, p in todo if os.path.exists(os.path.join(models, p)))
    log(f"done: {len(todo) - len(missing)}/{len(todo)} files, {total / 1e9:.0f} GB in {models}")
    for p in missing:
        log(f"MISSING {p}")
    sys.exit(1 if missing else 0)


if __name__ == "__main__":
    main()
