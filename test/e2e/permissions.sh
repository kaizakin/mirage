#!/usr/bin/env bash
# Tool permissions and the SSRF guard, against the deployed gateway:
#   - by default a member cannot see or call a store's create_product; an admin can
#   - the refused call is audited as denied
#   - a rule allowing one member create_product on one store shows the tool to that member
#     and lets the call through to the store; deleting the rule takes it away again
#   - an admin cannot add a server whose URL points inside the cluster or at cloud metadata
# It uses the first connected store server and creates a throwaway member.
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io), GATEWAY_URL
# (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
work=$(mktemp -d)
member_id=""
rule_id=""

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
pass() { log "ok    $*"; }
fail() {
  log "FAIL  $*"
  exit 1
}
secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
admin_key=$(secret "$RELEASE_NAME-gateway" admin-api-key)
cleanup() {
  [[ -n $rule_id ]] && curl -sS -o /dev/null -b "$work/admin" -X DELETE "$dash/api/tool-rules/$rule_id" || true
  [[ -n $member_id ]] && curl -sS -o /dev/null -b "$work/admin" -X DELETE "$dash/api/members/$member_id" || true
  rm -rf "$work"
}
trap cleanup EXIT

# mcp <key> <json>: one request in a fresh session; prints the JSON-RPC response.
mcp() {
  local session
  session=$(curl -sS -D - -o /dev/null -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' "$gateway" \
    -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"permissions-e2e","version":"1"}}}' |
    tr -d '\r' | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')
  curl -sS -H "Authorization: Bearer $1" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H "Mcp-Session-Id: $session" "$gateway" -d "$2" | sed -n 's/^data: //p'
}
tools() { mcp "$1" '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq -r '.result.tools[].name'; }
# Arguments that fail validation in the store, so an allowed call reaches it without creating a product.
create_call() {
  mcp "$1" "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"${server}__create_product\",\"arguments\":{\"name\":\"\",\"regular_price\":\"-1\"}}}"
}

curl -sS -o /dev/null -c "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/login" \
  -d "$(jq -n --arg p "$(secret "$RELEASE_NAME-gateway" admin-password)" '{email: "admin@mirage.local", password: $p}')"
server=$(curl -sS -b "$work/admin" "$dash/api/servers" | jq -r '[.[] | select(.store != null and .status == "connected")][0].slug // empty')
[[ -n $server ]] || fail "needs a connected store; create one on the dashboard's Stores page"
email="permissions-$RANDOM@example.com"
token=$(curl -sS -b "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/invites" -d "{\"email\":\"$email\",\"role\":\"member\"}" | jq -r .token)
member_id=$(curl -sS -c "$work/member" -H 'Content-Type: application/json' -X POST "$dash/api/invites/accept" \
  -d "{\"token\":\"$token\",\"password\":\"permissions e2e password\"}" | jq -r .id)
member_key=$(curl -sS -b "$work/member" -X POST "$dash/api/keys" | jq -r .key)
log "== store server $server, member $email"

log "== members are denied store writes by default"
tools "$admin_key" | grep -qx "${server}__create_product" || fail "the admin does not see ${server}__create_product"
pass "the admin sees ${server}__create_product"
member_tools=$(tools "$member_key")
grep -qx "${server}__list_orders" <<<"$member_tools" || fail "the member does not see ${server}__list_orders: $member_tools"
grep -qx "${server}__create_product" <<<"$member_tools" && fail "the member sees ${server}__create_product"
grep -qx "${server}__update_order_status" <<<"$member_tools" && fail "the member sees ${server}__update_order_status"
pass "the member sees list_orders but not create_product or update_order_status"
refusal=$(create_call "$member_key" | jq -r '.result.content[0].text')
[[ $refusal == "${server}__create_product is not permitted for your account"* ]] || fail "calling the hidden tool answered: $refusal"
pass "calling it anyway is refused: $refusal"
row=$(curl -sS -b "$work/member" "$dash/api/audit?limit=1" | jq -c '.entries[0] | {tool, outcome}')
[[ $row == '{"tool":"create_product","outcome":"denied"}' ]] || fail "the refusal was audited as $row"
pass "the refusal is in the audit log as denied"

log "== a per-user rule opens one tool on one store"
rule_id=$(curl -sS -b "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/tool-rules" \
  -d "{\"user_id\":$member_id,\"server\":\"$server\",\"tool\":\"create_product\",\"allow\":true}" | jq -r .id)
[[ $rule_id =~ ^[0-9]+$ ]] || fail "creating the rule answered $rule_id"
tools "$member_key" | grep -qx "${server}__create_product" || fail "the allowed tool is still hidden"
answer=$(create_call "$member_key" | jq -r '.result.content[0].text')
[[ $answer == "name must not be empty" ]] || fail "the allowed call did not reach the store: $answer"
pass "the member now sees create_product, and the call reached the store, which answered: $answer"
curl -sS -o /dev/null -b "$work/admin" -X DELETE "$dash/api/tool-rules/$rule_id"
rule_id=""
tools "$member_key" | grep -qx "${server}__create_product" && fail "the tool is still listed after its rule was deleted"
pass "deleting the rule hides the tool again"

log "== the SSRF guard"
for url in http://169.254.169.254/latest/meta-data/ "http://$RELEASE_NAME-postgres:5432/" http://10.0.0.1/mcp http://localhost:8080/mcp; do
  status=$(curl -sS -o "$work/body" -w '%{http_code}' -b "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/servers" \
    -d "{\"slug\":\"ssrf-probe\",\"url\":\"$url\",\"auth_type\":\"none\"}")
  [[ $status == 400 ]] || {
    curl -sS -o /dev/null -b "$work/admin" -X DELETE "$dash/api/servers/ssrf-probe"
    fail "adding a server at $url answered $status"
  }
  pass "$url refused: $(jq -r .error "$work/body")"
done

log "all permission checks passed"
