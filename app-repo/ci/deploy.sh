#!/usr/bin/env bash
# Roll the built image out to demo-prod. Orchestrator-agnostic, like build.sh.
#
# Stale-deploy guard: when GitHub Actions recovers, every job that sat in
# "queued" during the outage starts at once. Without this check they would
# redeploy commits older than what Tekton already shipped.
set -euo pipefail

GIT_SHA=${GIT_SHA:-$(git rev-parse HEAD)}
NAMESPACE=${NAMESPACE:-demo-prod}
DEPLOYED_BY=${DEPLOYED_BY:?set DEPLOYED_BY (github-actions|tekton)}
IMAGE=${IMAGE:-$(cat "${IMAGE_FILE:-.ci/image}")}
: "${DEMO_API_KEY:?DEMO_API_KEY missing: this secret must exist for every pipeline, not just GitHub}"

live=$(kubectl -n "$NAMESPACE" get deploy dr-demo-app \
  -o jsonpath='{.metadata.annotations.dr\.demo/git-sha}' 2>/dev/null || true)
if [[ -n "$live" ]]; then
  if [[ "$live" == "$GIT_SHA" ]]; then
    echo "already live: ${GIT_SHA:0:7} — nothing to do"; exit 0
  fi
  if git merge-base --is-ancestor "$GIT_SHA" "$live" 2>/dev/null; then
    echo "STALE: ${GIT_SHA:0:7} is an ancestor of live ${live:0:7} — refusing to roll back"; exit 0
  fi
fi

echo "--- deploying ${GIT_SHA:0:7} via $DEPLOYED_BY (was ${live:0:7})"
kubectl -n "$NAMESPACE" create secret generic dr-demo-app-secrets \
  --from-literal=DEMO_API_KEY="$DEMO_API_KEY" --dry-run=client -o yaml | kubectl apply -f -

sed -e "s|__IMAGE__|$IMAGE|g" \
    -e "s|__GIT_SHA__|$GIT_SHA|g" \
    -e "s|__DEPLOYED_BY__|$DEPLOYED_BY|g" \
    -e "s|__DEPLOYED_AT__|$(date -u +%Y-%m-%dT%H:%M:%SZ)|g" \
    deploy/app.yaml | kubectl -n "$NAMESPACE" apply -f -

kubectl -n "$NAMESPACE" rollout status deploy/dr-demo-app --timeout=120s
echo "live: ${GIT_SHA:0:7} (shipped by $DEPLOYED_BY)"
