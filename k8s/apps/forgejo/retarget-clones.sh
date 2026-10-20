#!/usr/bin/env bash
# Repoint local clones of migrated repos (decision z15k) from GitHub to
# Forgejo: every clone under $GIT_DIR_ROOT whose origin is
# github.com/<gh-owner>/<repo> and whose <repo> is in the list on stdin
# gets origin = http://git.gw.mesh.internal/<fj-owner>/<repo>.git and a
# `github` remote holding the old URL (the DR runbook's escape hatch,
# bootstrap.md section 6). Idempotent; -n prints only.
#
#   list-migrated | ./retarget-clones.sh [-n] [-r ~/git] [-g marnyg] [-o mar]
#   list-migrated | ssh host bash -s -- -r /home/x/git < retarget-clones.sh   # remote
set -euo pipefail
ROOT=$HOME/git GH_OWNER=marnyg FJ_OWNER=mar FJ=http://git.gw.mesh.internal DRY=0
while getopts "nr:g:o:" opt; do
  case $opt in n) DRY=1 ;; r) ROOT=$OPTARG ;; g) GH_OWNER=$OPTARG ;; o) FJ_OWNER=$OPTARG ;; *) exit 2 ;; esac
done
migrated=$(cat)   # one repo name per line

for d in "$ROOT"/*/; do
  [ -d "$d/.git" ] || continue
  url=$(git -C "$d" remote get-url origin 2>/dev/null) || continue
  name=$(sed -nE "s#^(https?://|git@)github\.com[:/]$GH_OWNER/([^/]+)/?\$#\2#p" <<<"$url"); name=${name%.git}
  [ -n "$name" ] || continue
  grep -qxF "$name" <<<"$migrated" || { echo "skip $(basename "$d"): $name not migrated"; continue; }
  new="$FJ/$FJ_OWNER/$name.git"
  echo "$(basename "$d"): origin $url -> $new"
  [ $DRY = 1 ] && continue
  git -C "$d" remote get-url github >/dev/null 2>&1 || git -C "$d" remote add github "$url"
  git -C "$d" remote set-url origin "$new"
done
