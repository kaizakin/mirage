#!/usr/bin/env bash
# The definition of done, end to end, with two users: A (the seeded admin, in the
# dashboard and its chat) and B (an invited member, through Claude Code with B's key).
# A provisions a fresh store for the run, and it is deleted at the end.
# Re-run it after every change; it exits non-zero on the first broken line.
#
# Needs the platform deployed with a chat model key (the <release>-groq Secret, or
# <release>-gemini with gateway.llm.provider=gemini), plus claude,
# jq, and curl on PATH.
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io),
# GATEWAY_URL (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
b_email="dod-b-$RANDOM@example.com"
b_id=""
product_name="DoD Mug $(date +%H%M%S)"
store="dod-$(date +%H%M%S)"
tools="store-${store}"
store_created=no

secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
step() { printf '\n%s  == %s\n' "$(date +%T)" "$*"; }
pass() { printf '%s  ok    %s\n' "$(date +%T)" "$*"; }
fail() {
  printf '%s  FAIL  %s\n' "$(date +%T)" "$*"
  exit 1
}

# api <jar> <method> <path> [json]: prints the body, fails on a non-2xx status.
api() {
  local jar=$1 method=$2 path=$3 body=${4:-}
  local args=(-sS -w '\n%{http_code}' -b "$jar" -c "$jar" -X "$method" "$dash$path")
  [[ -n $body ]] && args+=(-H 'Content-Type: application/json' -d "$body")
  local out status
  out=$(curl "${args[@]}")
  status=${out##*$'\n'}
  [[ $status == 2* ]] || fail "$method $path answered $status: ${out%$'\n'*}"
  printf '%s' "${out%$'\n'*}"
}

# chat <jar> <prompt>: runs one chat turn as the jar's user; prints the SSE events as JSON lines.
chat() {
  curl -sS -N -b "$1" -H 'Content-Type: application/json' -X POST "$dash/api/chat" \
    -d "$(jq -n --arg p "$2" '{messages: [{role: "user", content: $p}]}')" |
    awk '/^event: /{e=$2} /^data: /{sub(/^data: /,""); print "{\"event\":\"" e "\",\"data\":" $0 "}"}'
}

# claude_b <prompt>: one Claude Code turn with only the gateway, as B, allowed to use its tools.
claude_b() {
  timeout 300 claude -p "$1" --mcp-config "$work/b-mcp.json" --strict-mcp-config --allowedTools "mcp__gw" 2>&1
}

audit_since() { # audit_since <jar> <id>: audit rows newer than id, as JSON lines
  api "$1" GET "/api/audit?limit=200" | jq -c --argjson after "$2" '.entries[] | select(.id > $after)'
}

cleanup() {
  [[ -n $b_id ]] && api "$work/a" DELETE "/api/members/$b_id" >/dev/null 2>&1 || true
  [[ $store_created == yes ]] && api "$work/a" DELETE "/api/stores/$store" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

step "A logs in to the dashboard and creates a store; it is provisioned and connected"
api "$work/a" POST /api/login "$(jq -n --arg p "$(secret "$RELEASE_NAME-gateway" admin-password)" '{email: "admin@mirage.local", password: $p}')" >/dev/null
api "$work/a" POST /api/stores "{\"name\": \"DoD Tea $store\", \"slug\": \"$store\"}" >/dev/null
store_created=yes
for _ in {1..180}; do
  status=$(api "$work/a" GET /api/stores | jq -r --arg s "$store" '.[] | select(.slug == $s) | .status')
  [[ $status == ready ]] && break
  [[ $status == failed ]] && fail "store $store failed: $(api "$work/a" GET /api/stores | jq -r --arg s "$store" '.[] | select(.slug == $s) | .reason')"
  sleep 5
done
[[ $status == ready ]] || fail "store $store is still $status"
shop=$(api "$work/a" GET /api/stores | jq -r --arg s "$store" '.[] | select(.slug == $s) | .url')
for _ in {1..15}; do
  servers=$(api "$work/a" GET /api/servers)
  [[ $(jq -r --arg s "$tools" '.[] | select(.slug == $s) | .status' <<<"$servers") == connected ]] && break
  sleep 2
done
for slug in "$tools" currency deepwiki; do
  [[ $(jq -r ".[] | select(.slug == \"$slug\") | .status" <<<"$servers") == connected ]] || fail "$slug is not connected: $servers"
done
pass "store $store is Ready at $shop; $tools, currency, and deepwiki are connected"
audit_start=$(api "$work/a" GET "/api/audit?limit=1" | jq '.entries[0].id // 0')

step "A invites B as a member; B accepts, is refused admin pages, and generates a key"
token=$(api "$work/a" POST /api/invites "{\"email\": \"$b_email\", \"role\": \"member\"}" | jq -r .token)
b_id=$(api "$work/b" POST /api/invites/accept "{\"token\": \"$token\", \"password\": \"b's long password\"}" | jq -r .id)
status=$(curl -sS -o /dev/null -w '%{http_code}' -b "$work/b" "$dash/api/servers")
[[ $status == 403 ]] || fail "B got $status on /api/servers, want 403"
b_key=$(api "$work/b" POST /api/keys | jq -r .key)
[[ $b_key == gw_* ]] || fail "B's key response had no plaintext key"
pass "B ($b_email) is a member with a key, and gets 403 on servers"

step "Claude Code connects with B's key and lists the gateway's tools"
jq -n --arg url "$gateway" --arg auth "Bearer $b_key" \
  '{mcpServers: {gw: {type: "http", url: $url, headers: {Authorization: $auth}}}}' >"$work/b-mcp.json"
tools=$(claude_b "List the names of every tool you have from the gw MCP server, one per line, nothing else.")
for t in "${tools}__list_orders" currency__convert deepwiki__ask_wiki_question; do
  grep -q "$t" <<<"$tools" || fail "Claude Code (B) does not list $t: $tools"
done
pass "Claude Code (B) lists the store's, currency's, and deepwiki's tools"

step "A creates a product through the chat; it appears on the storefront"
events=$(chat "$work/a" "Create a product named \"$product_name\" priced 349 in the $store store.")
grep -q "\"name\":\"${tools}__create_product\"" <<<"$events" || fail "chat did not call ${tools}__create_product: $events"
grep -q '"event":"answer"' <<<"$events" || fail "chat gave no answer: $events"
slug=$(tr '[:upper:] ' '[:lower:]-' <<<"$product_name")
storefront=no
for _ in {1..10}; do
  # Captured first: grep -q closes the pipe at its first match, and under pipefail curl's
  # resulting write error would turn a match into a failure.
  page=$(curl -sS "$shop/product/$slug/")
  if grep -qF "$product_name" <<<"$page"; then storefront=yes; break; fi
  sleep 1
done
[[ $storefront == yes ]] || fail "$product_name is not on the storefront at $shop/product/$slug/"
pass "\"$product_name\" was created by chat and is on the storefront"

step "An order for it is placed, as A would at the storefront checkout"
order_id=$(STORE=$store "$here/place-order.sh" "$product_name")
pass "order #$order_id placed"

step "A asks the chat what orders came in today and gets that order"
events=$(chat "$work/a" "What orders came in today at the $store store?")
grep -q "\"name\":\"${tools}__list_orders\"" <<<"$events" || fail "chat did not call ${tools}__list_orders: $events"
jq -r 'select(.event == "answer") | .data.text' <<<"$events" | grep "$order_id" >/dev/null ||
  fail "the answer does not mention order $order_id: $(jq -r 'select(.event == "answer") | .data.text' <<<"$events")"
pass "the chat's answer includes order #$order_id"

step "B asks Claude Code a two-server question"
answer=$(claude_b "List today's orders in the $store store with their totals in USD.")
grep -q "$order_id" <<<"$answer" || fail "Claude Code (B) answer lacks order $order_id: $answer"
pass "Claude Code (B) answered with order #$order_id"

step "The audit log shows each call under the user who made it"
rows=$(audit_since "$work/a" "$audit_start")
jq -e --arg u admin@mirage.local 'select(.user_email == $u and .tool == "create_product" and .outcome == "ok")' <<<"$rows" >/dev/null ||
  fail "no ok create_product row under A: $rows"
jq -e --arg u admin@mirage.local 'select(.user_email == $u and .tool == "list_orders")' <<<"$rows" >/dev/null ||
  fail "no list_orders row under A: $rows"
jq -e --arg u "$b_email" --arg s "$tools" 'select(.user_email == $u and .server == $s and .tool == "list_orders")' <<<"$rows" >/dev/null ||
  fail "no $tools list_orders row under B: $rows"
jq -e --arg u "$b_email" 'select(.user_email == $u and .server == "currency" and .tool == "convert")' <<<"$rows" >/dev/null ||
  fail "no currency convert row under B: the two-server prompt did not use currency: $rows"
[[ -z $(jq -r 'select(.user_email == "")' <<<"$rows") ]] || fail "rows without a user: $rows"
pass "A's chat calls are A's, B's Claude Code calls (the store's and currency's) are B's, and none are anonymous"

step "B's key is revoked; Claude Code is refused"
api "$work/b" DELETE "/api/keys/${b_key:3:8}" >/dev/null
status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $b_key" -H 'Content-Type: application/json' "$gateway" -d '{}')
[[ $status == 401 ]] || fail "revoked key got $status, want 401"
pass "B's revoked key gets 401"

step "Role checks and failure drills"
"$here/api-roles.sh" | tail -1
"$here/drills.sh" | tail -1

printf '\n%s  definition of done: all green\n' "$(date +%T)"
