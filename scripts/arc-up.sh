#!/usr/bin/env bash
# actions-runner-controller + a runner scale set for the demo repo.
source "$(dirname "$0")/lib.sh"
require_env DEMO_REPO
ARC_VERSION=${ARC_VERSION:-0.14.2}
charts=oci://ghcr.io/actions/actions-runner-controller-charts

log "ARC controller $ARC_VERSION"
h upgrade --install arc "$charts/gha-runner-scale-set-controller" --version "$ARC_VERSION" -n arc-systems --wait
log "runner scale set dr-demo-runners -> https://github.com/$DEMO_REPO"
h upgrade --install dr-demo-runners "$charts/gha-runner-scale-set" --version "$ARC_VERSION" -n arc-runners \
  -f "$ROOT/deploy/arc/values.yaml" --set githubConfigUrl="https://github.com/$DEMO_REPO" --wait
