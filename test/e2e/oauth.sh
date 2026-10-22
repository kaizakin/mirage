#!/usr/bin/env bash
# Browser sign-in for MCP clients (the MCP authorization flow), against the deployed gateway,
# with the dashboard's consent step done through its API as a signed-in user:
#   - an unauthenticated /mcp request answers 401 naming the protected resource metadata
#   - discovery leads to the authorization server, where a client registers itself
#   - approving as the admin returns a code; exchanging it with the PKCE verifier returns
#     tokens, and the access token works on /mcp as that user
#   - a replayed code and a reused refresh token are refused; the refresh token rotates
#   - revoking the app in the dashboard signs the client out at once
#
# Env: NAMESPACE, RELEASE_NAME; DASH_URL (default http://dash.127.0.0.1.nip.io), GATEWAY_URL
# (default http://gateway.127.0.0.1.nip.io/mcp).
set -euo pipefail

dash=${DASH_URL:-http://dash.127.0.0.1.nip.io}
gateway=${GATEWAY_URL:-http://gateway.127.0.0.1.nip.io/mcp}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
redirect="http://127.0.0.1:43123/callback"

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
pass() { log "ok    $*"; }
fail() {
  log "FAIL  $*"
  exit 1
}
secret() { kubectl -n "$NAMESPACE" get secret "$1" -o "jsonpath={.data.$2}" | base64 -d; }

# mcp_status <token>: the HTTP status of an MCP initialize with that bearer token.
mcp_status() {
  curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' "$gateway" \
    -d '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"oauth-e2e","version":"1"}}}'
}

log "== discovery"
challenge=$(curl -sS -D - -o /dev/null -X POST "$gateway" -H 'Content-Type: application/json' -d '{}' | tr -d '\r' | awk -F': ' 'tolower($1) == "www-authenticate" {print $2}')
prm=$(sed -n 's/.*resource_metadata="\([^"]*\)".*/\1/p' <<<"$challenge")
[[ -n $prm ]] || fail "the 401 from /mcp names no resource metadata: $challenge"
issuer=$(curl -sS "$prm" | jq -r '.authorization_servers[0]')
meta=$(curl -sS "$issuer/.well-known/oauth-authorization-server")
token_url=$(jq -r .token_endpoint <<<"$meta")
pass "401 -> $prm -> issuer $issuer"

log "== registration"
client=$(curl -sS -X POST "$(jq -r .registration_endpoint <<<"$meta")" -H 'Content-Type: application/json' \
  -d "{\"client_name\":\"oauth e2e\",\"redirect_uris\":[\"$redirect\"],\"token_endpoint_auth_method\":\"none\"}" | jq -r .client_id)
[[ $client == mcp_* ]] || fail "registration returned client_id '$client'"
pass "registered client $client"

log "== consent, as the signed-in admin"
curl -sS -o /dev/null -c "$work/jar" -H 'Content-Type: application/json' -X POST "$dash/api/login" \
  -d "$(jq -n --arg p "$(secret "$RELEASE_NAME-gateway" admin-password)" '{email: "admin@mirage.local", password: $p}')"
verifier=$(head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 64)
pkce=$(printf %s "$verifier" | openssl dgst -sha256 -binary | base64 | tr '+/' '-_' | tr -d '=')
query=$(jq -rn --arg c "$client" --arg r "$redirect" --arg p "$pkce" --arg res "${gateway}" \
  '{response_type: "code", client_id: $c, redirect_uri: $r, code_challenge: $p, code_challenge_method: "S256", state: "e2e-state", resource: $res} | to_entries | map("\(.key)=\(.value | @uri)") | join("&")')
info=$(curl -sS -b "$work/jar" "$dash/api/oauth/authorize?$query")
name=$(jq -r .client_name <<<"$info")
[[ $name == "oauth e2e" ]] || fail "the consent page would show $info"
back=$(curl -sS -b "$work/jar" -H 'Content-Type: application/json' -H "Origin: $dash" -X POST "$dash/api/oauth/authorize" \
  -d "$(jq -n --arg q "$query" '{query: $q, approve: true}')" | jq -r .redirect)
[[ $back == "$redirect?"* ]] || fail "approval redirects to '$back'"
code=$(sed -n 's/.*[?&]code=\([^&]*\).*/\1/p' <<<"$back")
grep 'state=e2e-state' >/dev/null <<<"$back" || fail "the redirect lost the state: $back"
pass "approved; redirected to the client with a code and its state"

log "== token exchange"
exchange() {
  curl -sS -X POST "$token_url" --data-urlencode grant_type=authorization_code --data-urlencode "code=$code" \
    --data-urlencode "client_id=$client" --data-urlencode "redirect_uri=$redirect" --data-urlencode "code_verifier=$1"
}
tokens=$(exchange "$verifier")
access=$(jq -r .access_token <<<"$tokens")
refresh=$(jq -r .refresh_token <<<"$tokens")
[[ $access == gwo_* && $refresh == gwr_* ]] || fail "token response: $tokens"
[[ $(exchange "$verifier" | jq -r .error) == invalid_grant ]] || fail "a replayed code was accepted"
pass "exchanged the code (expires in $(jq .expires_in <<<"$tokens")s); replaying it is refused"

[[ $(mcp_status "$access") == 200 ]] || fail "the access token does not open /mcp"
pass "the access token opens /mcp"

log "== refresh rotation"
refreshed=$(curl -sS -X POST "$token_url" --data-urlencode grant_type=refresh_token --data-urlencode "refresh_token=$refresh" --data-urlencode "client_id=$client")
access2=$(jq -r .access_token <<<"$refreshed")
[[ $access2 == gwo_* ]] || fail "refresh: $refreshed"
again=$(curl -sS -X POST "$token_url" --data-urlencode grant_type=refresh_token --data-urlencode "refresh_token=$refresh" --data-urlencode "client_id=$client" | jq -r .error)
[[ $again == invalid_grant ]] || fail "a used refresh token worked again"
pass "refreshing issues new tokens; the old refresh token is refused"

log "== revoke from the dashboard"
app=$(curl -sS -b "$work/jar" "$dash/api/oauth/apps" | jq -r '.[] | select(.client_name == "oauth e2e") | .id' | head -1)
[[ -n $app ]] || fail "the app is not under Connected apps"
[[ $(curl -sS -o /dev/null -w '%{http_code}' -b "$work/jar" -H "Origin: $dash" -X DELETE "$dash/api/oauth/apps/$app") == 204 ]] || fail "revoke"
[[ $(mcp_status "$access2") == 401 ]] || fail "the access token still works after revoking the app"
pass "after revoking the app, its access token gets 401"

log "all OAuth checks passed"
