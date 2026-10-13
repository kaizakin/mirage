#!/usr/bin/env bash
# Runs the internal/woo live test against the store, using the application password the setup hook published.
#
# Env: NAMESPACE, RELEASE_NAME, CREDENTIALS_SECRET, LIVE_PORT.
set -euo pipefail

secret_field() {
  kubectl -n "$NAMESPACE" get secret "$CREDENTIALS_SECRET" -o "jsonpath={.data.$1}" | base64 -d
}

if ! WOO_PASSWORD=$(secret_field password) || [[ -z $WOO_PASSWORD ]]; then
  echo "no password in $NAMESPACE/$CREDENTIALS_SECRET; has the setup hook run?" >&2
  exit 1
fi
WOO_USERNAME=$(secret_field username)
export WOO_USERNAME WOO_PASSWORD
export WOO_BASE_URL="http://localhost:$LIVE_PORT"

kubectl -n "$NAMESPACE" port-forward "svc/$RELEASE_NAME-wordpress" "$LIVE_PORT:80" >/dev/null &
port_forward=$!
trap 'kill "$port_forward"' EXIT
for _ in {1..30}; do
  curl -sf -o /dev/null "$WOO_BASE_URL/wp-json/" && break
  sleep 1
done

go test ./internal/woo/ -run TestLiveStore -count=1 -v
