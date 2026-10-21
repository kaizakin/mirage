#!/usr/bin/env bash
# Store provisioning, end to end, against the deployed platform:
#   - two stores created at once both become Ready, each in its own namespace with its own
#     database, Secrets, volumes, Ingress, ResourceQuota, and LimitRange
#   - creating a store again is safe: the same store comes back, nothing is duplicated
#   - a user is held to the per-user store limit
#   - each store's MCP server is registered in the gateway as store-<slug>__<tool>, and works
#   - one store's MCP server can reach its own WordPress but not another store's WordPress
#     or MySQL (NetworkPolicies)
#   - a provisioner restart mid-provisioning still ends with the store Ready
#   - deleting a store removes its namespace, volumes, Secrets, and tools
# It exits non-zero on the first behavior that does not hold, and deletes what it made.
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io), GATEWAY_URL
# (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
work=$(mktemp -d)
run=$RANDOM
a="e2e-a-$run" b="e2e-b-$run" c="e2e-c-$run"
member_id=""

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
pass() { log "ok    $*"; }
fail() {
  log "FAIL  $*"
  exit 1
}
secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }
admin_key=$(secret "$RELEASE_NAME-gateway" admin-api-key)

# api <jar> <method> <path> [json]: prints "<status> <body>".
api() {
  local args=(-sS -o "$work/body" -w '%{http_code}' -b "$1" -c "$1" -X "$2" "$dash$3")
  [[ -n ${4:-} ]] && args+=(-H 'Content-Type: application/json' -d "$4")
  local status
  status=$(curl "${args[@]}")
  printf '%s %s' "$status" "$(cat "$work/body")"
}
store_status() { api "$work/admin" GET /api/stores | cut -d' ' -f2- | jq -r --arg s "$1" '.[] | select(.slug == $s) | .status'; }
store_field() { api "$work/admin" GET /api/stores | cut -d' ' -f2- | jq -r --arg s "$1" --arg f "$2" '.[] | select(.slug == $s) | .[$f]'; }

# wait_for <slug> <status> <seconds>: waits for a store to reach a status ("gone" = no row).
wait_for() {
  local deadline=$((SECONDS + $3)) now
  while ((SECONDS < deadline)); do
    now=$(store_status "$1")
    [[ $2 == gone && -z $now ]] && return 0
    [[ $now == "$2" ]] && return 0
    [[ $now == failed && $2 != failed ]] && fail "store $1 failed: $(store_field "$1" reason)"
    sleep 5
  done
  fail "store $1 is ${now:-gone} after $3s, want $2"
}

tools() {
  local sid
  sid=$(curl -sS -D - -o /dev/null -H "Authorization: Bearer $admin_key" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' "$gateway" \
    -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"stores-e2e","version":"1"}}}' |
    tr -d '\r' | awk -F': ' 'tolower($1) == "mcp-session-id" {print $2}')
  curl -sS -H "Authorization: Bearer $admin_key" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H "Mcp-Session-Id: $sid" "$gateway" -d "${1:-{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}}" | sed -n 's/^data: //p'
}

cleanup() {
  for s in "$a" "$b" "$c" "lim-1-$run" "lim-2-$run" "lim-3-$run"; do
    api "$work/admin" DELETE "/api/stores/$s" >/dev/null 2>&1 || true
  done
  [[ -n $member_id ]] && api "$work/admin" DELETE "/api/members/$member_id" >/dev/null 2>&1 || true
  kubectl -n "store-$a" delete pod isolation-probe --ignore-not-found --wait=false >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

[[ $(api "$work/admin" POST /api/login "$(jq -n --arg p "$(secret "$RELEASE_NAME-gateway" admin-password)" '{email: "admin@mirage.local", password: $p}')") == 200* ]] ||
  fail "admin login"

log "== two stores at once"
start=$SECONDS
for s in "$a" "$b"; do
  out=$(api "$work/admin" POST /api/stores "{\"name\":\"Store $s\",\"slug\":\"$s\"}")
  [[ $out == 201* ]] || fail "create $s: $out"
done
[[ $(store_status "$a") == provisioning && $(store_status "$b") == provisioning ]] || fail "new stores are not Provisioning"
pass "created $a and $b; both Provisioning"

out=$(api "$work/admin" POST /api/stores "{\"name\":\"Store $a\",\"slug\":\"$a\"}")
[[ $out == 200* ]] || fail "creating $a again answered $out, want 200 with the same store"
[[ $(api "$work/admin" GET /api/stores | cut -d' ' -f2- | jq --arg s "$a" '[.[] | select(.slug == $s)] | length') == 1 ]] || fail "creating again duplicated the store"
pass "creating $a again returns it unchanged (200), with no duplicate"

log "== the per-user limit"
token=$(api "$work/admin" POST /api/invites '{"email":"stores-e2e-'"$run"'@example.com","role":"member"}' | cut -d' ' -f2- | jq -r .token)
member=$(api "$work/member" POST /api/invites/accept "{\"token\":\"$token\",\"password\":\"stores e2e password\"}")
member_id=$(cut -d' ' -f2- <<<"$member" | jq -r .id)
for i in 1 2 3; do
  out=$(api "$work/member" POST /api/stores "{\"name\":\"Limit $i\",\"slug\":\"lim-$i-$run\"}")
  [[ $out == 201* ]] || fail "member's store $i: $out"
done
out=$(api "$work/member" POST /api/stores "{\"name\":\"Limit 4\",\"slug\":\"lim-4-$run\"}")
[[ $out == 409* ]] || fail "a fourth store answered $out, want 409"
pass "a member's fourth store is refused: $(cut -d' ' -f2- <<<"$out" | jq -r .error)"
for i in 1 2 3; do
  [[ $(api "$work/member" DELETE "/api/stores/lim-$i-$run") == 202* ]] || fail "member deleting store $i"
done
[[ $(api "$work/member" GET /api/stores | cut -d' ' -f2- | jq --arg a "$a" '[.[] | select(.slug == $a)] | length') == 0 ]] ||
  fail "a member can see the admin's store"
pass "the member deleted their stores while provisioning, and cannot see anyone else's"

wait_for "$a" ready 900
wait_for "$b" ready 900
pass "$a and $b are Ready after $((SECONDS - start))s"

log "== what each store got"
for s in "$a" "$b"; do
  ns="store-$s"
  got=$(kubectl -n "$ns" get statefulset,deployment,pvc,secret,ingress,resourcequota,limitrange,networkpolicy -o name | sed 's|/.*||' | sort | uniq -c | tr -s ' ' | tr '\n' ',')
  for want in statefulset.apps persistentvolumeclaim secret ingress.networking.k8s.io resourcequota limitrange networkpolicy.networking.k8s.io; do
    grep "$want" >/dev/null <<<"$got" || fail "$ns has no $want: $got"
  done
  host=$(kubectl -n "$ns" get ingress -o jsonpath='{.items[0].spec.rules[0].host}')
  [[ $host == "$s".* ]] || fail "$ns ingress host is $host"
  pass "$ns:$got host $host"
done
quota=$(kubectl -n "store-$a" get resourcequota store -o jsonpath='{.status.used.requests\.memory}/{.status.hard.requests\.memory}')
pass "store-$a quota in use (memory requests): $quota"
code=$(curl -sS -o /dev/null -w '%{http_code}' "$(store_field "$a" url)/")
[[ $code == 200 ]] || fail "$a's storefront answered $code"
pass "$a's storefront answers 200 at $(store_field "$a" url)"

log "== MCP registration"
list=$(tools | jq -r '.result.tools[].name')
for s in "$a" "$b"; do
  grep "^store-${s}__list_orders\$" >/dev/null <<<"$list" || fail "store-${s}__list_orders is not in tools/list"
done
products=$(tools '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"store-'"$a"'__list_products","arguments":{}}}' |
  jq -r '.result.structuredContent.products | length')
[[ $products -ge 12 ]] || fail "store-${a}__list_products returned $products products"
pass "tools/list has store-${a}__* and store-${b}__*; store-${a}__list_products returned $products products"

log "== isolation"
# A pod with woo-mcp's labels in store A gets exactly the network rules store A's MCP server has.
kubectl -n "store-$a" run isolation-probe --image=busybox:1.36 --restart=Never \
  --labels=app.kubernetes.io/name=woo-mcp --command -- sleep 300 >/dev/null
kubectl -n "store-$a" wait --for=condition=Ready pod/isolation-probe --timeout=60s >/dev/null
probe() { kubectl -n "store-$a" exec isolation-probe -- sh -c "$1" >/dev/null 2>&1 && echo open || echo blocked; }
own=$(probe "wget -q -T 5 -O /dev/null http://store-wordpress.store-$a.svc.cluster.local/")
other_wp=$(probe "wget -q -T 5 -O /dev/null http://store-wordpress.store-$b.svc.cluster.local/")
other_db=$(probe "nc -z -w 5 store-mysql.store-$b.svc.cluster.local 3306")
[[ $own == open ]] || fail "store A's MCP server cannot reach its own WordPress"
[[ $other_wp == blocked && $other_db == blocked ]] || fail "store A's MCP server reaches store B: wordpress $other_wp, mysql $other_db"
pass "from store A's MCP server: own WordPress $own; store B's WordPress $other_wp; store B's MySQL $other_db"
kubectl -n "store-$a" delete pod isolation-probe --wait=false >/dev/null

log "== a provisioner restart mid-provisioning"
[[ $(api "$work/admin" POST /api/stores "{\"name\":\"Store $c\",\"slug\":\"$c\"}") == 201* ]] || fail "create $c"
for _ in {1..30}; do
  kubectl -n "store-$c" get deployment store-woo-mcp >/dev/null 2>&1 && break
  sleep 2
done
kubectl -n "$NAMESPACE" delete pod -l app.kubernetes.io/name=provisioner --wait=true >/dev/null
pass "restarted the provisioner while $c was $(store_status "$c")"
wait_for "$c" ready 900
pass "$c still became Ready"

log "== teardown"
for s in "$a" "$b" "$c"; do
  [[ $(api "$work/admin" DELETE "/api/stores/$s") == 202* ]] || fail "delete $s"
done
for s in "$a" "$b" "$c"; do wait_for "$s" gone 600; done
for s in "$a" "$b" "$c"; do
  kubectl get namespace "store-$s" >/dev/null 2>&1 && fail "namespace store-$s still exists"
  [[ -z $(kubectl get pv -o jsonpath="{.items[?(@.spec.claimRef.namespace==\"store-$s\")].metadata.name}") ]] || fail "store-$s left volumes behind"
done
list=$(tools | jq -r '.result.tools[].name')
grep "^store-\($a\|$b\|$c\)__" >/dev/null <<<"$list" && fail "deleted stores' tools are still listed"
pass "namespaces, volumes, Secrets, and tools of $a, $b, and $c are gone"

log "all store checks passed"
