#!/usr/bin/env bash
# Migrate every GitHub repo of $GH_OWNER into Forgejo (full migration:
# code, issues, PRs, labels, milestones, releases, wiki) and add a push
# mirror back to GitHub, so Forgejo is the source of truth and GitHub a
# write replica (decision talos-config-z15k, amends 4la8).
#
# The push mirror is sync-on-commit with interval 0: a Forgejo restart
# never fires a sync, only a new push does. That is the DR escape hatch
# (bootstrap.md §6): fix via GitHub while Forgejo is down, then push the
# fix to Forgejo first thing when it is back.
#
# Credentials:
#   gh auth login            reads the GitHub side (repo list, migration import)
#   GH_MIRROR_TOKEN          the PAT Forgejo stores to push the mirror; taken
#                            from the sealed secret forgejo/github-mirror-token
#                            (bootstrap.md section 6) unless set in the environment.
#                            Not `gh auth token`: that dies with the gh login.
#   FORGEJO_TOKEN            Forgejo PAT: write:repository,write:issue,read:user.
#                            Unset: basic auth as sysadmin from the sealed
#                            secret forgejo/forgejo-sysadmin (an admin may
#                            migrate into any owner; -o is then required).
#
# Usage:
#   ./migrate-from-github.sh [-n] [-f] [-o forgejo-owner] [repo ...]
#     -n  dry run       -f  include forks      -a  include archived
#     no repo args = every repo of $GH_OWNER
#
# Idempotent: an existing Forgejo repo is left alone (no re-migration),
# an existing push mirror to the same address is not duplicated.
# A migration call blocks until the repo is imported; run this in the
# background with a log for large accounts.
set -euo pipefail

FORGEJO_URL=${FORGEJO_URL:-http://git.gw.mesh.internal}
GH_OWNER=${GH_OWNER:-$(gh api user -q .login)}
FORGEJO_OWNER=${FORGEJO_OWNER:-}
DRY=0 FORKS=0 ARCHIVED=0

while getopts "nfao:" opt; do
  case $opt in
    n) DRY=1 ;; f) FORKS=1 ;; a) ARCHIVED=1 ;; o) FORGEJO_OWNER=$OPTARG ;;
    *) exit 2 ;;
  esac
done
shift $((OPTIND - 1))

if [ -n "${FORGEJO_TOKEN:-}" ]; then
  FJ_AUTH=(-H "Authorization: token $FORGEJO_TOKEN")
else
  FJ_AUTH=(-u "$(kubectl -n forgejo get secret forgejo-sysadmin -o jsonpath='{.data.username}' | base64 -d):$(kubectl -n forgejo get secret forgejo-sysadmin -o jsonpath='{.data.password}' | base64 -d)")
fi
GH_TOKEN=$(gh auth token)                 # import side only
if [ -z "${GH_MIRROR_TOKEN:-}" ]; then  # push-mirror side: the vault entry
  GH_MIRROR_TOKEN=$(kubectl -n forgejo get secret github-mirror-token -o jsonpath='{.data.token}' | base64 -d)
  GH_MIRROR_USER=$(kubectl -n forgejo get secret github-mirror-token -o jsonpath='{.data.username}' | base64 -d)
fi
GH_MIRROR_USER=${GH_MIRROR_USER:-$GH_OWNER}
: "${GH_MIRROR_TOKEN:?no GH_MIRROR_TOKEN and no forgejo/github-mirror-token secret}"

fj() { # method path [json]
  curl -fsS -X "$1" "${FJ_AUTH[@]}" \
    -H 'Content-Type: application/json' "$FORGEJO_URL/api/v1$2" ${3:+-d "$3"}
}
fj_status() { # method path -> http code only
  curl -s -o /dev/null -w '%{http_code}' -X "$1" "${FJ_AUTH[@]}" "$FORGEJO_URL/api/v1$2"
}

if [ -z "$FORGEJO_OWNER" ]; then
  if [ -n "${FORGEJO_TOKEN:-}" ]; then
    FORGEJO_OWNER=$(fj GET /user | jq -r .login)
  else # sysadmin auth: the owner is the one wallet-registered (OIDC, source_id!=0) user
    FORGEJO_OWNER=$(fj GET /admin/users | jq -r '[.[] | select(.source_id != 0) | .login] | if length == 1 then .[0] else empty end')
    : "${FORGEJO_OWNER:?pass -o <owner>: not exactly one OIDC user to default to}"
  fi
fi
echo "github:$GH_OWNER -> $FORGEJO_URL/$FORGEJO_OWNER  (dry=$DRY forks=$FORKS archived=$ARCHIVED)"

if [ $# -gt 0 ]; then
  repos=$(printf '%s\n' "$@")
else
  repos=$(gh repo list "$GH_OWNER" --limit 1000 \
    --json name,isFork,isArchived \
    -q ".[] | select($FORKS==1 or .isFork|not) | select($ARCHIVED==1 or .isArchived|not) | .name")
fi

for name in $repos; do
  meta=$(gh api "repos/$GH_OWNER/$name" -q '{private:.private,description:(.description//""),has_wiki:.has_wiki}')
  private=$(jq -r .private <<<"$meta")
  desc=$(jq -r .description <<<"$meta")
  wiki=$(jq -r .has_wiki <<<"$meta")

  if [ "$(fj_status GET "/repos/$FORGEJO_OWNER/$name")" = 200 ]; then
    echo "== $name: exists on Forgejo, skipping migration"
  else
    echo "== $name: migrating (private=$private)"
    body=$(jq -n --arg o "$FORGEJO_OWNER" --arg n "$name" --arg d "$desc" \
      --arg addr "https://github.com/$GH_OWNER/$name.git" --arg tok "$GH_TOKEN" \
      --argjson priv "$private" --argjson wiki "$wiki" \
      '{service:"github",clone_addr:$addr,auth_token:$tok,repo_owner:$o,repo_name:$n,
        description:$d,private:$priv,mirror:false,
        issues:true,labels:true,milestones:true,pull_requests:true,releases:true,wiki:$wiki}')
    [ $DRY = 1 ] || fj POST /repos/migrate "$body" >/dev/null
  fi

  addr="https://github.com/$GH_OWNER/$name.git"
  if [ $DRY = 0 ] && fj GET "/repos/$FORGEJO_OWNER/$name/push_mirrors" | jq -e --arg a "$addr" 'any(.remote_address==$a)' >/dev/null; then
    echo "   push mirror -> github already present"
    continue
  fi
  echo "   adding push mirror -> $addr (sync_on_commit, interval 0)"
  body=$(jq -n --arg addr "$addr" --arg u "$GH_MIRROR_USER" --arg p "$GH_MIRROR_TOKEN" \
    '{remote_address:$addr,remote_username:$u,remote_password:$p,interval:"0",sync_on_commit:true}')
  [ $DRY = 1 ] || fj POST "/repos/$FORGEJO_OWNER/$name/push_mirrors" "$body" >/dev/null
  [ $DRY = 1 ] || fj POST "/repos/$FORGEJO_OWNER/$name/push_mirrors-sync" >/dev/null || true
done

echo "done. Next: repoint clones — git remote set-url origin $FORGEJO_URL/$FORGEJO_OWNER/<repo>.git"
