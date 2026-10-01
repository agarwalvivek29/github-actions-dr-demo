#!/usr/bin/env bash
# Bring the whole demo up (idempotent). Without GitHub App credentials in .env
# it stops after the local pieces; run it again after create-github-app.py.
source "$(dirname "$0")/lib.sh"
"$ROOT/scripts/cluster-up.sh"
"$ROOT/scripts/tekton-up.sh"
"$ROOT/scripts/build-images.sh"

log "base, Tekton pipeline, fake status page"
k apply -f "$ROOT/deploy/base/namespaces.yaml" >/dev/null
k apply -f "$ROOT/deploy/base/" -f "$ROOT/deploy/tekton/" -f "$ROOT/deploy/fake-status/" >/dev/null

if [[ -z "${GITHUB_APP_ID:-}" ]]; then
  warn "no GitHub App yet — next: ./scripts/create-github-app.py --repo <owner>/<app-repo>"
  exit 0
fi
"$ROOT/scripts/sync-secrets.sh"
"$ROOT/scripts/arc-up.sh"
log "dr-controller + webhook relay"
k apply -f "$ROOT/deploy/controller/" -f "$ROOT/deploy/smee/" >/dev/null
k -n dr-system rollout restart deploy/dr-controller deploy/fake-status deploy/webhook-relay >/dev/null
k -n dr-system rollout status deploy/dr-controller --timeout=120s >/dev/null
cat <<MSG

  demo app          http://localhost:8088
  dr-controller     http://localhost:9090
  status page (sim) http://localhost:9091
  Tekton dashboard  http://localhost:9097

MSG
