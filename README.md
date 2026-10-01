# Your Prod Has DR. Your Pipeline Doesn't.

Demo for the FOSS Mumbai talk: **failing releases over from GitHub Actions to
Tekton** when Actions is down, with an RTO under 15 minutes. Everything runs on
a laptop: a kind cluster hosts "prod", the self-hosted GitHub runners, Tekton,
and the failover controller.

```
                         push to main
                              │
            ┌─────────────────┴──────────────────┐
            ▼                                    ▼
   GitHub Actions scheduler            GitHub App webhook ──► smee.io ──► webhook-relay
   (queues the release job)            (and REST API, polled)                   │
            │                                                                   ▼
            ▼                          ┌──────────────────────────── dr-controller ──────────┐
   ARC runner (self-hosted,            │ detectors: status page + queue probe                │
   in-cluster) — waits on GitHub       │ switch:    dr-config mode=auto|tekton|gha           │
   to hand it a job                    │ triggers:  webhook, poller (fallback), break-glass  │
            │                          └───────────────┬─────────────────────────────────────┘
            │                                          ▼ (only when Tekton owns releases)
            │                                Tekton PipelineRun: clone → build → deploy
            │                                          │
            └──────► ci/build.sh + ci/deploy.sh ◄──────┘   same scripts, two orchestrators
                               │
                               ▼
                     demo-prod/dr-demo-app  (shows who shipped it)
```

## What's where

| Path | What it is |
|---|---|
| `app-repo/` | The dummy service repo ([agarwalvivek29/dr-demo-app](https://github.com/agarwalvivek29/dr-demo-app)): Go app, `.github/workflows/release.yml`, and the **orchestrator-agnostic** `ci/build.sh` / `ci/deploy.sh` |
| `controller/cmd/dr-controller` | The GitHub App: health detection, failover switch, webhook receiver, poller, commit statuses. Stdlib-only Go |
| `controller/cmd/fake-status` | Outage simulator: the real githubstatus.com feed with per-component overrides |
| `deploy/tekton/` | `git-clone` + `ci-script` Tasks and the `release` Pipeline |
| `deploy/controller/` | Controller Deployment, RBAC, **`dr-config` (the switch)** and detector tuning |
| `deploy/base/` | Namespaces and the one `deployer` Role bound to *both* release paths |
| `deploy/arc/` | actions-runner-controller runner scale set values |
| `scripts/` | `up.sh`, `sim.sh` (outage simulator), `drill.sh` (recorded RTO drill), secret sync, App creation |

## How it decides

Two detectors, because they fail differently (`controller/cmd/dr-controller/health.go`):

1. **Status page**: Statuspage v2 `components.json`, reading the `Actions` and `Webhooks`
   components. It's the authoritative source but it lags: incidents are often
   declared well after jobs have started queueing.
2. **Queue probe**: how long has *our* release workflow been `queued` with no
   runner assigned? This is a first-party signal that leads the status page.
   (Demo threshold 60s; in prod you'd use ~10m.)

In `mode: auto`, either detector tripping moves releases to Tekton. Failback
happens only after Actions has been healthy for `FAILBACK_COOLDOWN` (hysteresis).
`mode: tekton` / `mode: gha` are the manual override, for when you declare the incident yourself:

```sh
kubectl --context kind-gha-dr -n dr-system patch cm dr-config -p '{"data":{"mode":"tekton"}}'
```

On failover the controller ships the current branch head right away. Everything
pushed before the switch is stuck in GitHub's queue.

### Triggers

- **Webhook** (`push`), HMAC-verified, relayed through smee.io.
- **Poller**: `GET /repos/{repo}/commits/main` every few seconds using the App
  token. It gives the webhook a grace period and stops waiting as soon as the
  status page reports a Webhooks incident. The REST API is usually still up
  during Actions and Webhooks incidents, and that's why polling works as the fallback.
- **Break glass**: `POST localhost:9090/trigger`.

All three feed one idempotent path: one PipelineRun per sha (label
`dr.demo/git-sha`), serialized, with a latest-wins queue, which matches the GHA
workflow's `concurrency` group.

### What the second path had to bring with it

The following only existed inside GitHub before, and each one needed a replacement:

| In GitHub Actions | On the Tekton path |
|---|---|
| `GITHUB_TOKEN` for checkout | App installation token, minted per run, in a Secret owned by the PipelineRun |
| `secrets.DEMO_API_KEY` | `ci/release-secrets`, synced from one `.env` by `scripts/sync-secrets.sh` (which also sets the GitHub secret) |
| Runner's deploy permissions | The same `deployer` Role, bound to the Tekton ServiceAccount too |
| Checks on the commit | `dr/tekton-release` commit status posted by the controller |
| `concurrency: release-prod` | Controller serializes runs |

### The recovery trap

When Actions recovers, every job queued during the outage starts at once.
Without protection those jobs redeploy commits *older* than the one Tekton
already shipped. `ci/deploy.sh` refuses when the live sha already contains the
sha being deployed (`git merge-base --is-ancestor`).

## Running it

Prereqs: Docker, kind, kubectl, helm, gh, jq, Go 1.25+.
All commands pin `--context kind-gha-dr`.

```sh
./scripts/up.sh                                   # cluster, registry, Tekton, images
./scripts/create-app-repo.sh --private            # DEMO_REPO in .env (done once)
./scripts/create-github-app.py --repo agarwalvivek29/dr-demo-app   # click Create, then Install
./scripts/up.sh                                   # again: secrets, ARC runners, controller
```

| URL | |
|---|---|
| http://localhost:8088 | the app: shows the live commit and **who shipped it** |
| http://localhost:9090 | dr-controller: current path, signals, timeline |
| http://localhost:9091 | the drill status page (click to break GitHub) |
| http://localhost:9097 | Tekton Dashboard |

## Simulating outages

We can't break GitHub's scheduler, so we reproduce its symptom: GitHub accepts
and queues the job, and no runner is ever handed one. In the GitHub UI and the
API this looks exactly like the real incident, and it's the symptom the queue
probe measures. The status page stays green unless you flip it too, which is
also how real incidents usually start.

```sh
./scripts/sim.sh actions on        # jobs stay queued (ARC scale set -> 0 runners)
./scripts/sim.sh webhooks on       # push events stop arriving (relay -> 0)
./scripts/sim.sh statuspage Actions major_outage
./scripts/sim.sh mode tekton|gha|auto
./scripts/sim.sh state
./scripts/sim.sh reset
```

To detect from the real status page, set `STATUS_URL` in
`deploy/controller/controller.yaml` to `https://www.githubstatus.com/api/v2/components.json`.

## Recorded drills

```sh
./scripts/drill.sh actions     # push a hotfix during an Actions outage, measure push -> live
./scripts/drill.sh webhooks    # already failed over + webhooks down: the poller must catch it
```

Each run writes `drills/<timestamp>-<scenario>.json` with the timeline and RTO.

## Tear down

```sh
./scripts/down.sh     # the GitHub App and repo are left in place
```
