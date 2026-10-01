#!/usr/bin/env bash
# Create the demo app repo on GitHub from app-repo/ and push it.
source "$(dirname "$0")/lib.sh"
require_env DEMO_REPO
visibility=${1:---public}
work="$ROOT/.work/app"
rm -rf "$work" && mkdir -p "$ROOT/.work" && cp -R "$ROOT/app-repo" "$work"
git -C "$work" init -q -b main
git -C "$work" add -A
git -C "$work" commit -qm "dr-demo-app: service + GitHub Actions release workflow"
gh repo create "$DEMO_REPO" "$visibility" --description "Demo service for the GitHub Actions -> Tekton DR talk" \
  --source "$work" --remote origin --push
log "https://github.com/$DEMO_REPO"
