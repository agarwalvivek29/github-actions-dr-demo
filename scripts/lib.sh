# Shared helpers. Every kubectl/helm call is pinned to the demo cluster so a
# stray current-context can never point this at a real environment.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER=gha-dr
CTX="kind-${CLUSTER}"
REG_NAME=kind-registry
REG_PORT=5001

# Local config (GitHub App id, repo, secrets). See .env.example.
if [[ -f "$ROOT/.env" ]]; then set -a; source "$ROOT/.env"; set +a; fi

k()    { kubectl --context "$CTX" "$@"; }
h()    { helm --kube-context "$CTX" "$@"; }
log()  { printf '\033[1;36m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m!!  %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31mxx  %s\033[0m\n' "$*" >&2; exit 1; }

require_env() {
  for v in "$@"; do [[ -n "${!v:-}" ]] || die "$v is not set (see .env.example)"; done
}
