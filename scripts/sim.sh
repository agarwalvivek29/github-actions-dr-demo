#!/usr/bin/env bash
# Outage simulator.
#
#   sim.sh actions on|off         GitHub stops handing jobs to runners (they sit in "queued").
#                                 The status page is NOT touched: like a real incident, it lags.
#   sim.sh webhooks on|off        push webhooks stop arriving (relay scaled to 0)
#   sim.sh statuspage <Component> <operational|degraded_performance|partial_outage|major_outage>
#   sim.sh mode auto|tekton|gha   flip the manual switch
#   sim.sh state                  what the dr-controller thinks right now
#   sim.sh reset                  everything back to healthy, mode=auto
#
# We can't break GitHub's scheduler, so we reproduce its symptom: jobs are
# accepted and queued, and no runner is ever handed one. From GitHub's UI and
# from the API that's indistinguishable from the real thing — and the queue
# probe measures exactly that symptom.
source "$(dirname "$0")/lib.sh"
STATUS=${STATUS_PAGE:-http://localhost:9091}
CONTROLLER=${CONTROLLER:-http://localhost:9090}

runners() {
  k -n arc-runners patch autoscalingrunnerset dr-demo-runners --type merge \
    -p "{\"spec\":{\"minRunners\":$1,\"maxRunners\":$2}}" >/dev/null
}
statuspage() {
  local st=${2:-operational}; [[ "$st" == clear ]] && st=operational
  curl -fsS -X POST "$STATUS/api/override?component=$1&status=$st"
  log "status page: $1=$st"
}
mode() {
  k -n dr-system patch cm dr-config --type merge -p "{\"data\":{\"mode\":\"$1\"}}" >/dev/null
  log "switch: mode=$1"
}

case "${1:-}" in
  actions)
    case "${2:-}" in
      on)  runners 0 0; log "Actions outage ON — new jobs will stay queued (status page still green)";;
      off) runners 1 2; log "Actions outage OFF — queued jobs will now drain";;
      *) die "sim.sh actions on|off";;
    esac;;
  webhooks)
    case "${2:-}" in
      on)  k -n dr-system scale deploy webhook-relay --replicas=0 >/dev/null
           k -n dr-system wait --for=delete pod -l app=webhook-relay --timeout=60s >/dev/null 2>&1 || true
           log "Webhooks outage ON — push events are no longer delivered";;
      off) k -n dr-system scale deploy webhook-relay --replicas=1 >/dev/null; log "Webhooks outage OFF";;
      *) die "sim.sh webhooks on|off";;
    esac;;
  statuspage) statuspage "${2:?component}" "${3:?status}";;
  mode) mode "${2:?auto|tekton|gha}";;
  state)
    curl -fsS "$CONTROLLER/state" | jq '{mode, path, reasons, head: .head_sha[0:7], live: .live_sha[0:7], live_by,
      running: .running_run, queued_runs: .signals.queued_runs,
      oldest_queued_s: ((.signals.oldest_queued_ns // 0) / 1e9 | floor),
      status_actions: .signals.status_actions, status_webhooks: .signals.status_webhooks}';;
  reset)
    runners 1 2
    k -n dr-system scale deploy webhook-relay --replicas=1 >/dev/null
    statuspage Actions operational; statuspage Webhooks operational
    mode auto;;
  *) sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; exit 1;;
esac
