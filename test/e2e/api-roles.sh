#!/usr/bin/env bash
# Checks the dashboard API's authorization and secrecy against the deployed gateway:
#   - the session cookie is HttpOnly and SameSite=Lax
#   - a member gets 403 on every server and member endpoint, and can manage only their own keys
#   - an admin can manage servers, and disabling one takes its tools out of /mcp within seconds
#   - no response body ever contains a credential: neither the sentinel this script stores
#     nor the real upstream credentials held in the cluster's Secrets
# It creates a throwaway member and server and removes them when it ends.
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io),
# GATEWAY_URL (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
admin_password=$(secret "$RELEASE_NAME-gateway" admin-password)
admin_key=$(secret "$RELEASE_NAME-gateway" admin-api-key)
sentinel="SENTINEL-credential-$RANDOM$RANDOM"
work=$(mktemp -d)
bodies="$work/bodies"
member_id=""
scratch=""
failures=0

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
check() { # check <description> <expected> <actual>
  if [[ $2 == "$3" ]]; then log "ok    $1 ($3)"; else log "FAIL  $1: want $2, got $3"; failures=$((failures + 1)); fi
}

# api <jar> <method> <path> [json] prints the status and appends the body to $bodies.
api() {
  local jar=$1 method=$2 path=$3 body=${4:-}
  local args=(-sS -o "$work/last" -w '%{http_code}' -b "$jar" -c "$jar" -X "$method" "$dash$path")
  [[ -n $body ]] && args+=(-H 'Content-Type: application/json' -d "$body")
  local status
  status=$(curl "${args[@]}")
  cat "$work/last" >>"$bodies"
  echo >>"$bodies"
  echo "$status"
}

cleanup() {
  [[ -n $scratch ]] && api "$work/admin" DELETE "/api/servers/$scratch" >/dev/null || true
  [[ -n $member_id ]] && api "$work/admin" DELETE "/api/members/$member_id" >/dev/null || true
  api "$work/admin" PATCH /api/servers/deepwiki '{"enabled":true}' >/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

mcp_tools() {
  local sid
  sid=$(curl -sS -D - -o /dev/null -H "Authorization: Bearer $admin_key" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' "$gateway" \
    -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"api-roles","version":"1"}}}' |
    tr -d '\r' | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')
  curl -sS -H "Authorization: Bearer $admin_key" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H "Mcp-Session-Id: $sid" "$gateway" -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' |
    sed -n 's/^data: //p' | jq -r '.result.tools[].name'
}

log "== admin"
cookie=$(curl -sS -D - -o /dev/null -c "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/login" \
  -d "$(jq -n --arg p "$admin_password" '{email:"admin@mirage.local",password:$p}')" | tr -d '\r' | grep -i '^set-cookie: mirage_session=')
check "session cookie is HttpOnly" yes "$(grep -qi 'httponly' <<<"$cookie" && echo yes || echo no)"
check "session cookie is SameSite=Lax" yes "$(grep -qi 'samesite=lax' <<<"$cookie" && echo yes || echo no)"
check "wrong password" 401 "$(api "$work/nobody" POST /api/login '{"email":"admin@mirage.local","password":"wrong"}')"
check "admin lists servers" 200 "$(api "$work/admin" GET /api/servers)"
scratch="scratch-$RANDOM"
check "admin creates a server with a credential" 201 \
  "$(api "$work/admin" POST /api/servers "{\"slug\":\"$scratch\",\"url\":\"http://scratch.invalid/mcp\",\"auth_type\":\"bearer\",\"credential\":\"$sentinel\"}")"
check "admin rotates its credential" 204 "$(api "$work/admin" PATCH "/api/servers/$scratch" "{\"credential\":\"$sentinel-2\"}")"
check "the server lists with has_credential" true \
  "$(api "$work/admin" GET /api/servers >/dev/null; jq -r ".[] | select(.slug == \"$scratch\") | .has_credential" "$work/last")"

log "== member"
check "admin invites a member" 201 "$(api "$work/admin" POST /api/invites '{"email":"member-check@example.com","role":"member"}')"
token=$(jq -r .token "$work/last")
check "member accepts the invite" 201 "$(api "$work/member" POST /api/invites/accept "{\"token\":\"$token\",\"password\":\"correct horse battery\"}")"
member_id=$(jq -r .id "$work/last")
check "the invite cannot be reused" 404 "$(api "$work/other" POST /api/invites/accept "{\"token\":\"$token\",\"password\":\"correct horse battery\"}")"
check "member: GET /api/me" 200 "$(api "$work/member" GET /api/me)"
for request in "GET /api/servers" "POST /api/servers" "PATCH /api/servers/woo" "DELETE /api/servers/woo" \
  "GET /api/members" "PATCH /api/members/1" "DELETE /api/members/1" "GET /api/invites" "POST /api/invites"; do
  read -r method path <<<"$request"
  check "member: $request" 403 "$(api "$work/member" "$method" "$path" '{}')"
done
check "member generates a key" 201 "$(api "$work/member" POST /api/keys)"
member_key_id=$(jq -r .id "$work/last")
check "the key's plaintext is returned once" yes "$(jq -r '.key | startswith("gw_")' "$work/last" | sed 's/true/yes/')"
check "listing keys never shows plaintext" 0 "$(api "$work/member" GET /api/keys >/dev/null; jq '[.[] | select(has("key"))] | length' "$work/last")"
check "member sees only their own keys, even with ?all=1" 1 "$(api "$work/member" GET '/api/keys?all=1' >/dev/null; jq length "$work/last")"
check "member cannot revoke the admin's key" 404 "$(api "$work/member" DELETE "/api/keys/${admin_key:3:8}")"
check "member revokes their own key" 204 "$(api "$work/member" DELETE "/api/keys/$member_key_id")"
check "member's audit view is only their own" 0 \
  "$(api "$work/member" GET /api/audit >/dev/null; jq '[.entries[] | select(.user_email != "member-check@example.com")] | length' "$work/last")"
old_session=$(awk '$6 == "mirage_session" {print $7}' "$work/member")
check "member logs out" 204 "$(api "$work/member" POST /api/logout)"
# Replayed by hand: the jar dropped the cookie, so this proves the server ended the session.
check "the old session token is refused after logout" 401 \
  "$(curl -sS -o /dev/null -w '%{http_code}' -b "mirage_session=$old_session" "$dash/api/me")"

log "== disabling a server reaches /mcp"
check "admin disables deepwiki" 204 "$(api "$work/admin" PATCH /api/servers/deepwiki '{"enabled":false}')"
gone=no
for _ in {1..10}; do
  if ! mcp_tools | grep -q '^deepwiki__'; then gone=yes; break; fi
  sleep 0.5
done
check "deepwiki's tools leave /mcp within 5s" yes "$gone"
check "admin re-enables deepwiki" 204 "$(api "$work/admin" PATCH /api/servers/deepwiki '{"enabled":true}')"

log "== no response body contains a credential"
credentials=("$sentinel" "$(secret "$RELEASE_NAME-woo-mcp" token)" "$(secret "$RELEASE_NAME-currency-mcp" apiKey)")
leaks=0
for c in "${credentials[@]}"; do
  if grep -qF -- "$c" "$bodies"; then leaks=$((leaks + 1)); fi
done
check "credentials found in $(wc -l <"$bodies") response bodies" 0 "$leaks"

((failures == 0)) || { log "$failures check(s) failed"; exit 1; }
log "all checks passed"
