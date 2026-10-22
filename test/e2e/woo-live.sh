#!/usr/bin/env bash
# Runs the internal/woo live test against a provisioned store, using the application password
# its setup Job published.
#
# Env: STORE (the store's slug), LIVE_PORT.
set -euo pipefail

: "${STORE:?set STORE to the slug of a provisioned store, e.g. make test-live STORE=tea-house}"
ns="store-$STORE"
secret_field() {
  kubectl -n "$ns" get secret woo-api-credentials -o "jsonpath={.data.$1}" | base64 -d
}

if ! WOO_PASSWORD=$(secret_field password) || [[ -z $WOO_PASSWORD ]]; then
  echo "no password in $ns/woo-api-credentials; is store $STORE ready?" >&2
  exit 1
fi
WOO_USERNAME=$(secret_field username)
export WOO_USERNAME WOO_PASSWORD
export WOO_BASE_URL="http://localhost:$LIVE_PORT"

kubectl -n "$ns" port-forward svc/store-wordpress "$LIVE_PORT:80" >/dev/null &
port_forward=$!
trap 'kill "$port_forward"' EXIT
for _ in {1..30}; do
  curl -sf -o /dev/null "$WOO_BASE_URL/wp-json/" && break
  sleep 1
done

go test ./internal/woo/ -run TestLiveStore -count=1 -v
