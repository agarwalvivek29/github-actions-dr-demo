#!/usr/bin/env bash
# Test + build + push. Orchestrator-agnostic: GitHub Actions and Tekton both run
# exactly this script, so failing over never means rewriting the build.
set -euo pipefail

GIT_SHA=${GIT_SHA:-$(git rev-parse HEAD)}
REGISTRY=${REGISTRY:-kind-registry:5000}
IMAGE_FILE=${IMAGE_FILE:-.ci/image}

echo "--- go test"
go test ./...

echo "--- ko build ${GIT_SHA:0:7} -> ${REGISTRY}"
export KO_DOCKER_REPO="${REGISTRY}/dr-demo-app"
image=$(ko build --bare --insecure-registry --platform=linux/"$(go env GOARCH)" --tags "$GIT_SHA" .)

mkdir -p "$(dirname "$IMAGE_FILE")"
echo "$image" > "$IMAGE_FILE"
echo "built $image"
