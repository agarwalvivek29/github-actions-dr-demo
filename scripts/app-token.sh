#!/usr/bin/env bash
# Print the App's installation id (or a JWT with --jwt) using only openssl + curl.
source "$(dirname "$0")/lib.sh"
require_env GITHUB_APP_ID GITHUB_APP_PRIVATE_KEY DEMO_REPO
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
now=$(date +%s)
header=$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)
payload=$(printf '{"iat":%d,"exp":%d,"iss":"%s"}' $((now - 60)) $((now + 540)) "$GITHUB_APP_ID" | b64url)
sig=$(printf '%s.%s' "$header" "$payload" | openssl dgst -sha256 -sign "$ROOT/$GITHUB_APP_PRIVATE_KEY" | b64url)
jwt="$header.$payload.$sig"
if [[ "${1:-}" == --jwt ]]; then echo "$jwt"; exit 0; fi
curl -fsS -H "Authorization: Bearer $jwt" -H "Accept: application/vnd.github+json" \
  "https://api.github.com/repos/$DEMO_REPO/installation" | jq -er .id \
  || die "App $GITHUB_APP_ID is not installed on $DEMO_REPO"
