#!/usr/bin/env bash
# Tear down the demo cluster and registry (the GitHub App and repo are left alone).
source "$(dirname "$0")/lib.sh"
kind delete cluster --name "$CLUSTER"
docker rm -f "$REG_NAME" >/dev/null 2>&1 || true
