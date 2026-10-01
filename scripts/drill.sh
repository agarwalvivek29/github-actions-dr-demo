#!/usr/bin/env bash
# Recorded DR drill: break GitHub, ship a hotfix anyway, measure RTO.
#
#   drill.sh actions    Actions scheduler outage. Push a hotfix while no job can be
#                       dispatched; the queue probe must fail over and Tekton ship it.
#   drill.sh webhooks   Already failed over, and now push webhooks stop too. Nothing
#                       tells us about the push; the poller must catch it.
#
# Options: --keep (leave the outage on afterwards)
# Results land in drills/<timestamp>-<scenario>.json.
source "$(dirname "$0")/lib.sh"
require_env DEMO_REPO
scenario=${1:?usage: drill.sh actions|webhooks [--keep]}
keep=${2:-}
CONTROLLER=${CONTROLLER:-http://localhost:9090}
TARGET_RTO=${TARGET_RTO:-900}
sim="$ROOT/scripts/sim.sh"
work="$ROOT/.work/app"

state() { curl -fsS "$CONTROLLER/state"; }
now() { date +%s; }
stamp() { date -r "$1" +%H:%M:%S; }

log "preflight"
"$sim" reset >/dev/null
for _ in $(seq 1 60); do
  s=$(state); [[ $(jq -r .path <<<"$s") == github-actions ]] && break; sleep 2
done
[[ $(jq -r .path <<<"$s") == github-actions ]] || die "controller is not on the GitHub Actions path yet: $(jq -c .reasons <<<"$s")"
if [[ -d "$work/.git" ]]; then git -C "$work" pull -q --ff-only; else git clone -q "git@github.com:$DEMO_REPO.git" "$work"; fi

t_outage=$(now)
case "$scenario" in
  actions)
    "$sim" actions on ;;
  webhooks)
    "$sim" actions on
    "$sim" mode tekton          # we've already declared the Actions incident
    "$sim" webhooks on ;;
  *) die "unknown scenario $scenario" ;;
esac

log "shipping a hotfix during the outage"
msg="hotfix shipped during the $scenario outage @ $(date +%H:%M:%S)"
sed -i.bak -E "s/^const message = .*/const message = \"$msg\"/" "$work/main.go" && rm "$work/main.go.bak"
git -C "$work" commit -qam "drill($scenario): $msg"
git -C "$work" push -q origin HEAD:main
sha=$(git -C "$work" rev-parse HEAD)
t_push=$(now)
log "pushed ${sha:0:7} — waiting for it to be live (target RTO ${TARGET_RTO}s)"

t_failover="" t_run="" t_live="" run="" trigger=""
while (( $(now) - t_push < TARGET_RTO + 300 )); do
  s=$(state)
  [[ -z "$t_failover" && $(jq -r .path <<<"$s") == tekton ]] && { t_failover=$(now); log "failed over: $(jq -r '.reasons|join("; ")' <<<"$s")"; }
  if [[ -z "$t_run" && $(jq -r .running_sha <<<"$s") == "$sha" ]]; then
    t_run=$(now); run=$(jq -r .running_run <<<"$s")
    trigger=$(k -n ci get pipelinerun "$run" -o jsonpath='{.metadata.labels.dr\.demo/trigger}')
    log "Tekton PipelineRun $run started (trigger: $trigger)"
  fi
  [[ $(jq -r .live_sha <<<"$s") == "$sha" ]] && { t_live=$(now); break; }
  sleep 2
done

rto=$(( ${t_live:-$(now)} - t_push ))
by=$(curl -fsS localhost:8088/version | jq -r .deployed_by)
echo
printf '  %-28s %s\n' "outage started" "$(stamp $t_outage)" "hotfix pushed" "$(stamp $t_push)  ${sha:0:7}"
[[ -n "$t_failover" ]] && printf '  %-28s %s  (+%ss)\n' "releases failed over" "$(stamp $t_failover)" $((t_failover - t_push))
[[ -n "$t_run" ]] && printf '  %-28s %s  (+%ss, trigger: %s)\n' "Tekton pipeline started" "$(stamp $t_run)" $((t_run - t_push)) "$trigger"
[[ -n "$t_live" ]] && printf '  %-28s %s  (+%ss, shipped by %s)\n' "hotfix live in prod" "$(stamp $t_live)" "$rto" "$by"
echo
if [[ -n "$t_live" ]] && (( rto <= TARGET_RTO )); then log "RTO ${rto}s — within the ${TARGET_RTO}s target"; ok=true
else warn "RTO target missed (${rto}s)"; ok=false; fi

mkdir -p "$ROOT/drills"
out="$ROOT/drills/$(date +%Y%m%d-%H%M%S)-$scenario.json"
jq -n --arg scenario "$scenario" --arg sha "$sha" --arg run "$run" --arg trigger "$trigger" --arg by "$by" \
  --argjson outage "$t_outage" --argjson push "$t_push" --argjson failover "${t_failover:-null}" \
  --argjson run_start "${t_run:-null}" --argjson live "${t_live:-null}" --argjson rto "$rto" \
  --argjson target "$TARGET_RTO" --argjson ok "$ok" \
  '{scenario:$scenario, sha:$sha, pipelinerun:$run, trigger:$trigger, shipped_by:$by, rto_seconds:$rto,
    target_seconds:$target, met_target:$ok,
    timeline:{outage:$outage, push:$push, failover:$failover, pipeline_start:$run_start, live:$live}}' > "$out"
log "recorded $out"

if [[ "$keep" == --keep ]]; then warn "outage left ON — run scripts/sim.sh reset to recover"; exit 0; fi
log "recovering: GitHub Actions comes back and drains its queue"
"$sim" reset
log "watch the queued run start and skip: deploy.sh refuses to redeploy ${sha:0:7} (already live) or anything older"
