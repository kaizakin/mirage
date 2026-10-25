#!/usr/bin/env bash
# Search mode against the deployed gateway: a client sending X-Mirage-Tools: search lists two
# tools, finds a store's tools by keyword, and calls one through mirage__call_tool, which is
# audited as the store's own tool.
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io), GATEWAY_URL
# (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
pass() { log "ok    $*"; }
fail() {
  log "FAIL  $*"
  exit 1
}
secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
key=$(secret "$RELEASE_NAME-gateway" admin-api-key)

headers=(-H "Authorization: Bearer $key" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H 'X-Mirage-Tools: search')
session=$(curl -sS -D - -o /dev/null "${headers[@]}" "$gateway" \
  -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"search-e2e","version":"1"}}}' |
  tr -d '\r' | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')
rpc() { curl -sS "${headers[@]}" -H "Mcp-Session-Id: $session" "$gateway" -d "$1" | sed -n 's/^data: //p'; }

listed=$(rpc '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq -r '[.result.tools[].name] | sort | join(" ")')
[[ $listed == "mirage__call_tool mirage__search_tools" ]] || fail "search mode listed: $listed"
pass "search mode lists two tools: $listed"

found=$(rpc '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"mirage__search_tools","arguments":{"query":"list orders"}}}' |
  jq -r '.result.structuredContent.tools[].name')
tool=$(grep -m1 '__list_orders$' <<<"$found" || true)
[[ $tool == store-*__list_orders ]] || fail "searching for 'list orders' found: $found"
pass "searching for 'list orders' found $(wc -l <<<"$found") tools, first store match $tool"

answer=$(rpc "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"mirage__call_tool\",\"arguments\":{\"name\":\"$tool\",\"arguments\":{}}}}")
jq -e '.result.isError | not' <<<"$answer" >/dev/null || fail "calling $tool through call_tool failed: $answer"
pass "mirage__call_tool ran $tool"

curl -sS -o /dev/null -c "$work/admin" -H 'Content-Type: application/json' -X POST "$dash/api/login" \
  -d "$(jq -n --arg p "$(secret "$RELEASE_NAME-gateway" admin-password)" '{email: "admin@mirage.local", password: $p}')"
row=$(curl -sS -b "$work/admin" "$dash/api/audit?limit=1" | jq -r '.entries[0] | "\(.server)__\(.tool) \(.outcome)"')
[[ $row == "$tool ok" ]] || fail "the audit log's latest row is $row, want $tool ok"
pass "the audit row names the store's tool: $row"

log "all search mode checks passed"
