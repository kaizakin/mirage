#!/usr/bin/env bash
# Failure drills against the deployed gateway, over one MCP session held open through the
# ingress, the way a real client holds it:
#   1. the woo-mcp pod dies mid-session: currency keeps working, woo fails cleanly, then recovers
#   2. currency__slow_echo(60) times out at the gateway's 20s limit while other calls proceed
#   3. a server disabled in the database disappears from tools/list, with tools/list_changed
#      pushed to the session; re-enabling brings it back
# It exits non-zero on the first behavior that does not hold.
#
# Env: NAMESPACE, RELEASE_NAME; GATEWAY_URL (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

url=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
key=$(kubectl -n "$NAMESPACE" get secret "$RELEASE_NAME-gateway" -o 'jsonpath={.data.admin-api-key}' | base64 -d)
work=$(mktemp -d)
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$work"' EXIT

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
fail() { log "FAIL: $*"; exit 1; }

# post sends one JSON-RPC message on the session and prints the response's JSON body.
post() {
  curl -sS -m 60 -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' ${session:+-H "Mcp-Session-Id: $session"} \
    "$url" -d "$1" | sed -n 's/^data: //p'
}

# call invokes a tool and prints "ok <text>" or "error <text>".
call() {
  post "{\"jsonrpc\":\"2.0\",\"id\":$RANDOM,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}" |
    jq -r '.result | (if .isError then "error " else "ok " end) + (.content[0].text // "" | .[0:160])'
}

tools() {
  post '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq -r '.result.tools[].name' | sort
}

psql_gateway() {
  kubectl -n "$NAMESPACE" exec "$RELEASE_NAME-postgres-0" -- psql -U mirage -d mirage -qtAc "$1"
}

session=$(curl -sS -D - -o /dev/null -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' "$url" \
  -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"drills","version":"1"}}}' |
  tr -d '\r' | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')
[[ -n $session ]] || fail "no session id from initialize"
post '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null
# The standalone stream is where the gateway pushes notifications to this session.
curl -sS -N -H "Authorization: Bearer $key" -H 'Accept: text/event-stream' -H "Mcp-Session-Id: $session" \
  "$url" >"$work/stream" 2>/dev/null &
log "session open, $(tools | wc -l) tools"

log "== drill 1: delete the woo-mcp pod mid-session"
kubectl -n "$NAMESPACE" delete pod -l app.kubernetes.io/name=woo-mcp --wait=false >/dev/null
sleep 2
currency=$(call currency__convert '{"amount":10,"from":"USD","to":"EUR"}')
woo=$(call woo__list_products '{}')
log "currency__convert   -> $currency"
log "woo__list_products  -> $woo"
[[ $currency == ok* ]] || fail "currency stopped working while woo was down"
[[ $woo == "error upstream woo "* ]] || fail "woo did not fail with a clean upstream error"
for _ in {1..30}; do
  woo=$(call woo__list_products '{}')
  [[ $woo == ok* ]] && break
  sleep 2
done
[[ $woo == ok* ]] || fail "woo did not recover"
log "woo__list_products  -> recovered on its own: ${woo:0:60}"

log "== drill 2: currency__slow_echo(60) against the 20s call timeout"
start=$SECONDS
call currency__slow_echo '{"seconds":60,"message":"too slow"}' >"$work/slow" &
slow_pid=$!
sleep 1
for _ in 1 2 3; do
  fast_start=$SECONDS
  fast=$(call currency__convert '{"amount":1,"from":"GBP","to":"USD"}')
  [[ $fast == ok* && $((SECONDS - fast_start)) -le 2 ]] || fail "a parallel call was held up: $fast"
done
log "3 parallel currency__convert calls answered while slow_echo was pending"
wait "$slow_pid"
slow=$(cat "$work/slow")
log "currency__slow_echo -> $slow (after $((SECONDS - start))s)"
[[ $slow == "error upstream currency did not answer slow_echo within 20s" ]] || fail "slow call did not time out cleanly"
((SECONDS - start >= 19 && SECONDS - start <= 25)) || fail "timeout fired at $((SECONDS - start))s, not about 20s"

log "== drill 3: disable deepwiki in the database"
# Counted, not diffed: diff exits 1 when the files differ, which pipefail would turn into a failure.
notifications() { grep -c 'notifications/tools/list_changed' "$work/stream" || true; }
before=$(notifications)
psql_gateway "UPDATE servers SET enabled = false WHERE slug = 'deepwiki'"
for _ in {1..20}; do
  ! tools | grep -q '^deepwiki__' && break
  sleep 1
done
tools | grep -q '^deepwiki__' && fail "deepwiki tools still listed after disabling it"
sleep 2
((before < $(notifications))) || fail "no tools/list_changed reached the session"
log "deepwiki gone from tools/list ($(tools | wc -l) tools); tools/list_changed pushed to the session"
psql_gateway "UPDATE servers SET enabled = true WHERE slug = 'deepwiki'"
for _ in {1..30}; do
  tools | grep -q '^deepwiki__' && break
  sleep 1
done
tools | grep -q '^deepwiki__' || fail "deepwiki did not come back after re-enabling it"
log "deepwiki re-enabled: back in tools/list ($(tools | wc -l) tools)"

log "== audit rows written during the drills"
psql_gateway "SELECT to_char(started_at, 'HH24:MI:SS') || '  ' || rpad(server_slug || '__' || tool, 22) || rpad(outcome, 12) || coalesce(left(error, 70), '')
  FROM audit_log WHERE started_at > now() - interval '5 minutes' ORDER BY id"
log "all drills passed"
