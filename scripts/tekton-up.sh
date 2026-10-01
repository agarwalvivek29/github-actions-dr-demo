#!/usr/bin/env bash
# Install Tekton Pipelines + Dashboard (pinned versions) into the demo cluster.
source "$(dirname "$0")/lib.sh"

TEKTON_PIPELINES_VERSION=${TEKTON_PIPELINES_VERSION:-v1.15.3}
TEKTON_DASHBOARD_VERSION=${TEKTON_DASHBOARD_VERSION:-v0.72.0}

log "installing Tekton Pipelines $TEKTON_PIPELINES_VERSION"
k apply -f "https://infra.tekton.dev/tekton-releases/pipeline/previous/${TEKTON_PIPELINES_VERSION}/release.yaml"
log "installing Tekton Dashboard $TEKTON_DASHBOARD_VERSION (read-only)"
k apply -f "https://infra.tekton.dev/tekton-releases/dashboard/previous/${TEKTON_DASHBOARD_VERSION}/release.yaml"

k -n tekton-pipelines wait --for=condition=Available deploy --all --timeout=300s
# Expose the dashboard on localhost:9097 via the kind port mapping.
k -n tekton-pipelines patch svc tekton-dashboard --type merge \
  -p '{"spec":{"type":"NodePort","ports":[{"name":"http","port":9097,"targetPort":9097,"nodePort":30097}]}}'
log "Tekton ready — dashboard at http://localhost:9097"
