#!/usr/bin/env bash
# Several gateway replicas behind the one /mcp endpoint, and per-user rate limits, against the
# deployed platform:
#   - the ingress spreads new MCP sessions over the replicas
#   - every request of a session is served by the replica that owns it, though the ingress
#     sends it to any replica (responses name their replica in X-Mirage-Replica)
#   - a member's calls past the per-minute limit are refused and audited as rate_limited
#   - when a session's replica dies, the session answers 404 and a new session works
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io), GATEWAY_URL
# (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
work=$(mktemp -d)
member_id=""

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
pass() { log "ok    $*"; }
fail() {
  log "FAIL  $*"
  exit 1
}
secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
admin_key=$(secret "$RELEASE_NAME-gateway" admin-api-key)
cleanup() {
  [[ -n $member_id ]] && curl -sS -o /dev/null -b "$work/admin" -X DELETE "$dash/api/members/$member_id" || true
  rm -rf "$work"
}
trap cleanup EXIT

# open <key>: starts a session; prints "<session id> <replica>".
open_session() {
  curl -sS -D - -o /dev/null -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' "$gateway" \
    -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"scaling-e2e","version":"1"}}}' |
    tr -d '\r' | awk -F': ' 'tolower($1) == "mcp-session-id" {s=$2} tolower($1) == "x-mirage-replica" {r=$2} END {print s, r}'
}
# send <key> <session> <json>: prints "<status> <replica>" and leaves the body in $work/body.
send() {
  curl -sS -D "$work/headers" -o "$work/body" -w '%{http_code}' -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' -H "Mcp-Session-Id: $2" "$gateway" -d "$3" >"$work/status"
  printf '%s %s' "$(cat "$work/status")" "$(tr -d '\r' <"$work/headers" | awk -F': ' 'tolower($1) == "x-mirage-replica" {print $2}')"
}
list='{"jsonrpc":"2.0","id":1,"method":"tools/list"}'

replicas=$(kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/name=gateway --field-selector=status.phase=Running -o name | wc -l)
((replicas >= 2)) || fail "need at least 2 gateway replicas, found $replicas"
log "== $replicas gateway replicas"

log "== the ingress spreads sessions"
declare -A owners
for _ in {1..10}; do
  read -r _ replica <<<"$(open_session "$admin_key")"
  owners[$replica]=1
done
((${#owners[@]} >= 2)) || fail "10 new sessions all went to one replica: ${!owners[*]}"
pass "10 new sessions were owned by ${#owners[@]} replicas: ${!owners[*]}"

log "== a session stays with its replica"
read -r session owner <<<"$(open_session "$admin_key")"
for i in {1..12}; do
  read -r status served <<<"$(send "$admin_key" "$session" "$list")"
  [[ $status == 200 ]] || fail "request $i of the session answered $status"
  [[ $served == "$owner" ]] || fail "request $i was served by $served; the session belongs to $owner"
done
pass "12 requests of one session were all served by its owner, $owner"

log "== per-user rate limit"
curl -sS -o /dev/null -c "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/login" \
  -d "$(jq -n --arg p "$(secret "$RELEASE_NAME-gateway" admin-password)" '{email: "admin@mirage.local", password: $p}')"
email="scaling-$RANDOM@example.com"
token=$(curl -sS -b "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/invites" -d "{\"email\":\"$email\",\"role\":\"member\"}" | jq -r .token)
member_id=$(curl -sS -c "$work/member" -H 'Content-Type: application/json' -X POST "$dash/api/invites/accept" \
  -d "{\"token\":\"$token\",\"password\":\"scaling e2e password\"}" | jq -r .id)
member_key=$(curl -sS -b "$work/member" -X POST "$dash/api/keys" | jq -r .key)
read -r msession _ <<<"$(open_session "$member_key")"
limit=$(kubectl -n "$NAMESPACE" get deploy "$RELEASE_NAME-gateway" -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="RATE_LIMIT_PER_MINUTE")].value}')
call='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"currency__convert","arguments":{"amount":1,"from":"USD","to":"INR"}}}'
refused=""
for i in $(seq 1 $((limit + 5))); do
  send "$member_key" "$msession" "$call" >/dev/null
  if sed -n 's/^data: //p' "$work/body" | jq -e '.result.isError' >/dev/null 2>&1; then
    refused=$i
    break
  fi
done
[[ -n $refused ]] || fail "no call was refused in $((limit + 5)) calls"
((refused > limit)) || fail "call $refused was refused; the limit is $limit"
pass "call $refused was refused: $(sed -n 's/^data: //p' "$work/body" | jq -r '.result.content[0].text')"
outcome=$(curl -sS -b "$work/member" "$dash/api/audit?limit=1" | jq -r '.entries[0].outcome')
[[ $outcome == rate_limited ]] || fail "the refused call was audited as $outcome"
pass "the refused call is in the audit log as rate_limited"

log "== a replica dies"
read -r session owner <<<"$(open_session "$admin_key")"
kubectl -n "$NAMESPACE" delete pod "$owner" --wait=false >/dev/null
for _ in {1..20}; do
  read -r status _ <<<"$(send "$admin_key" "$session" "$list")"
  [[ $status == 404 ]] && break
  sleep 1
done
[[ $status == 404 ]] || fail "the dead replica's session answered $status, want 404"
read -r fresh replica <<<"$(open_session "$admin_key")"
read -r status _ <<<"$(send "$admin_key" "$fresh" "$list")"
[[ $status == 200 ]] || fail "a new session after the replica died answered $status"
pass "after $owner died its session answered 404, and a new session on $replica works"

log "all scaling checks passed"
