#!/usr/bin/env bash
# Build demo images and push them to the local registry (localhost:5001 == kind-registry:5000 in-cluster).
source "$(dirname "$0")/lib.sh"
r="localhost:${REG_PORT}"
build() { log "image $1"; docker build -q -t "$r/$1:dev" "${@:2}" >/dev/null && docker push -q "$r/$1:dev" >/dev/null; }

build ci-tools      "$ROOT/images/ci-tools"
build actions-runner --build-arg CI_TOOLS="$r/ci-tools:dev" "$ROOT/images/runner"
build dr-controller "$ROOT/controller"
build smee-client   "$ROOT/images/smee"
log "images pushed to $r"
