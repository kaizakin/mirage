#!/usr/bin/env bash
# Runs the internal/store live migration test against the cluster's Postgres. The test
# migrates a throwaway database it creates and drops, never the gateway's own.
#
# Env: NAMESPACE, RELEASE_NAME, STORE_PORT.
set -euo pipefail

password=$(kubectl -n "$NAMESPACE" get secret "$RELEASE_NAME-postgres" -o 'jsonpath={.data.password}' | base64 -d)
export STORE_TEST_DSN="postgres://mirage:$password@localhost:$STORE_PORT/mirage?sslmode=disable"

kubectl -n "$NAMESPACE" port-forward "svc/$RELEASE_NAME-postgres" "$STORE_PORT:5432" >/dev/null &
port_forward=$!
trap 'kill "$port_forward"' EXIT
for _ in {1..30}; do
  (exec 3<>"/dev/tcp/localhost/$STORE_PORT") 2>/dev/null && break
  sleep 1
done

go test ./internal/store/ -run TestLiveMigrations -count=1 -v
