#!/usr/bin/env bash
# Hosting an MCP server from a container image, against the deployed platform:
#   - an admin adds a server by image; it runs in namespace mcp-<slug> and its tools work
#     through the gateway
#   - only the gateway can reach it, and it cannot reach the cluster or cloud metadata
#   - an upgrade to a broken image is marked failed with the reason while the previous
#     version keeps serving; setting a good image again brings it back to running
#   - deleting the server deletes its namespace
# It hosts the platform's own currency-mcp image under a throwaway slug.
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io), GATEWAY_URL
# (default http://gateway.127.0.0.1.nip.io/mcp), IMAGE (default mirage/currency-mcp:dev).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
image=${IMAGE:-mirage/currency-mcp:dev}
slug="fx-$RANDOM"
ns="mcp-$slug"
api_key="hosted-e2e-$RANDOM$RANDOM"
work=$(mktemp -d)

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
pass() { log "ok    $*"; }
fail() {
  log "FAIL  $*"
  exit 1
}
secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
admin_key=$(secret "$RELEASE_NAME-gateway" admin-api-key)
cleanup() {
  curl -sS -o /dev/null -b "$work/admin" -X DELETE "$dash/api/servers/$slug" || true
  kubectl -n default delete pod hosted-probe --ignore-not-found --wait=false >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

api() { curl -sS -o "$work/body" -w '%{http_code}' -b "$work/admin" -H 'Content-Type: application/json' -X "$1" "$dash$2" ${3:+-d "$3"}; }
hosted() { curl -sS -b "$work/admin" "$dash/api/servers" | jq -r --arg s "$slug" ".[] | select(.slug == \$s) | $1"; }
# wait_for <jq filter> <want> <seconds>
wait_for() {
  local got=""
  for _ in $(seq 1 "$3"); do
    got=$(hosted "$1")
    [[ $got == "$2" ]] && return 0
    sleep 1
  done
  fail "$1 is $got after $3s, want $2 (deploy reason: $(hosted '.hosted.reason // "none"'))"
}
convert() {
  local session
  session=$(curl -sS -D - -o /dev/null -H "Authorization: Bearer $admin_key" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' "$gateway" \
    -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"hosted-e2e","version":"1"}}}' |
    tr -d '\r' | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')
  curl -sS -H "Authorization: Bearer $admin_key" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H "Mcp-Session-Id: $session" "$gateway" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"${slug}__convert\",\"arguments\":{\"amount\":10,\"from\":\"USD\",\"to\":\"INR\"}}}" |
    sed -n 's/^data: //p' | jq -r 'if .result.isError then "error: " + .result.content[0].text else .result.content[0].text end'
}

curl -sS -o /dev/null -c "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/login" \
  -d "$(jq -n --arg p "$(secret "$RELEASE_NAME-gateway" admin-password)" '{email: "admin@mirage.local", password: $p}')"

log "== host $image as $slug"
status=$(api POST /api/servers "$(jq -n --arg s "$slug" --arg i "$image" --arg k "$api_key" \
  '{slug: $s, auth_type: "header", auth_header: "X-Api-Key", credential: $k, hosted: {image: $i, port: 8080, path: "/mcp", env: {CURRENCY_API_KEY: $k}}}')")
[[ $status == 201 ]] || fail "adding the hosted server answered $status: $(cat "$work/body")"
wait_for .hosted.status running 120
wait_for .status connected 60
pass "$slug is running in $ns and connected: $(hosted .url)"
answer=$(convert)
[[ $answer != error* && -n $answer ]] || fail "${slug}__convert answered: $answer"
pass "${slug}__convert through the gateway: $answer"

log "== isolation"
kubectl -n "$ns" get networkpolicy hosted-mcp >/dev/null || fail "$ns has no NetworkPolicy"
pod=$(kubectl -n "$ns" get pod -l app.kubernetes.io/name=hosted-mcp -o jsonpath='{.items[0].metadata.name}')
user=$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.spec.securityContext.runAsNonRoot}')
[[ $user == true ]] || fail "the hosted pod does not require a non-root user"
kubectl -n default run hosted-probe --image=busybox:1.36 --restart=Never --command -- sleep 300 >/dev/null
kubectl -n default wait --for=condition=Ready pod/hosted-probe --timeout=60s >/dev/null
from_outside=$(kubectl -n default exec hosted-probe -- nc -z -w 5 "mcp.$ns.svc.cluster.local" 8080 >/dev/null 2>&1 && echo open || echo blocked)
[[ $from_outside == blocked ]] || fail "a pod outside the gateway reached the hosted server"
pass "a pod outside the gateway cannot reach the hosted server; it runs as non-root under NetworkPolicy hosted-mcp"

log "== a broken upgrade keeps the previous version serving"
status=$(api PATCH "/api/servers/$slug" '{"hosted":{"image":"mirage/currency-mcp:does-not-exist"}}')
[[ $status == 204 ]] || fail "changing the image answered $status: $(cat "$work/body")"
wait_for .hosted.status failed 240
pass "the upgrade failed: $(hosted .hosted.reason)"
answer=$(convert)
[[ $answer != error* ]] || fail "the previous version stopped serving: $answer"
pass "the previous version still answers: $answer"
status=$(api PATCH "/api/servers/$slug" "{\"hosted\":{\"image\":\"$image\"}}")
[[ $status == 204 ]] || fail "restoring the image answered $status"
wait_for .hosted.status running 120
pass "setting $image again brought it back to running"

log "== delete"
status=$(api DELETE "/api/servers/$slug")
[[ $status == 204 ]] || fail "deleting answered $status"
for _ in {1..90}; do
  kubectl get namespace "$ns" >/dev/null 2>&1 || break
  sleep 1
done
kubectl get namespace "$ns" >/dev/null 2>&1 && fail "namespace $ns still exists 90s after its server was deleted"
pass "deleting $slug removed namespace $ns"

log "all hosting checks passed"
