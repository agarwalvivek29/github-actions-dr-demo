#!/usr/bin/env bash
# One source of truth (.env) -> every place a release path reads secrets from.
#
# This is the unglamorous half of DR: DEMO_API_KEY used to live only in GitHub
# Secrets, so a pipeline that doesn't run on GitHub couldn't deploy at all.
source "$(dirname "$0")/lib.sh"
require_env DEMO_REPO GITHUB_APP_ID GITHUB_APP_PRIVATE_KEY GITHUB_WEBHOOK_SECRET SMEE_URL DEMO_API_KEY
pem="$ROOT/$GITHUB_APP_PRIVATE_KEY"
apply_secret() { k create secret generic "$@" --dry-run=client -o yaml | k apply -f - >/dev/null; }

log "dr-system/github-app (dr-controller + webhook relay)"
apply_secret -n dr-system github-app \
  --from-literal=app-id="$GITHUB_APP_ID" --from-literal=repo="$DEMO_REPO" \
  --from-literal=webhook-secret="$GITHUB_WEBHOOK_SECRET" --from-literal=smee-url="$SMEE_URL" \
  --from-file=private-key.pem="$pem"

log "arc-runners/arc-github-app (self-hosted runner registration)"
installation=$("$ROOT/scripts/app-token.sh")
apply_secret -n arc-runners arc-github-app \
  --from-literal=github_app_id="$GITHUB_APP_ID" --from-literal=github_app_installation_id="$installation" \
  --from-file=github_app_private_key="$pem"

log "ci/release-secrets (Tekton deploy secrets)"
apply_secret -n ci release-secrets --from-literal=DEMO_API_KEY="$DEMO_API_KEY"

log "GitHub Actions secret DEMO_API_KEY on $DEMO_REPO"
gh secret set DEMO_API_KEY -R "$DEMO_REPO" --body "$DEMO_API_KEY"
