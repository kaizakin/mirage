#!/usr/bin/env bash
# The platform's least-privilege guardrails, against the deployed cluster:
#   - the provisioner's service account cannot read the platform's Secrets, cannot bind its
#     rights outside namespaces it created, and cannot create or delete other namespaces,
#     while binding inside a store namespace still works
#   - the platform namespace denies traffic by default: a pod with the gateway's labels reaches
#     Postgres and nothing else internal, and an unlabelled pod reaches nothing
#
# Env: NAMESPACE, RELEASE_NAME.
set -euo pipefail

sa="system:serviceaccount:$NAMESPACE:$RELEASE_NAME-provisioner"
probes=()

log() { printf '%s  %s\n' "$(date +%T)" "$*"; }
pass() { log "ok    $*"; }
fail() {
  log "FAIL  $*"
  exit 1
}
other="hardening-other-$RANDOM"
cleanup() {
  for p in "${probes[@]}"; do kubectl -n "$NAMESPACE" delete pod "$p" --ignore-not-found --wait=false >/dev/null 2>&1 || true; done
  kubectl delete namespace "$other" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

# refused <description> <kubectl args...>: the provisioner's attempt must fail.
refused() {
  local what=$1 out
  shift
  if out=$(kubectl --as="$sa" "$@" --dry-run=server 2>&1); then fail "the provisioner could $what: $out"; fi
  pass "the provisioner cannot $what: ${out##*: }"
}

log "== provisioner rights"
store_ns=$(kubectl get ns -l app.kubernetes.io/managed-by=mirage-provisioner,mirage/store -o jsonpath='{.items[0].metadata.name}')
[[ -n $store_ns ]] || fail "needs a store namespace; create a store on the dashboard's Stores page"
if out=$(kubectl --as="$sa" -n "$NAMESPACE" get secret "$RELEASE_NAME-gateway" 2>&1); then fail "the provisioner read the gateway's Secret"; fi
pass "the provisioner cannot read the platform's Secrets: ${out##*: }"
refused "bind its rights in the platform namespace" -n "$NAMESPACE" create rolebinding escalate \
  --clusterrole="$RELEASE_NAME-provisioner-tenant" --serviceaccount="$NAMESPACE:$RELEASE_NAME-provisioner"
refused "create a namespace not labelled as its own" create namespace "hardening-$RANDOM"
kubectl create namespace "$other" >/dev/null
refused "delete a namespace it did not create" delete namespace "$other"
kubectl --as="$sa" -n "$store_ns" create rolebinding hardening-probe --clusterrole="$RELEASE_NAME-provisioner-tenant" \
  --serviceaccount="$NAMESPACE:$RELEASE_NAME-provisioner" --dry-run=server >/dev/null ||
  fail "the provisioner cannot bind its rights in its own namespace $store_ns"
pass "the provisioner can still bind its rights in $store_ns"

log "== default-deny in $NAMESPACE"
# probe <name> <labels>: a pod that gets the network rules of pods with those labels.
probe() {
  probes+=("$1")
  kubectl -n "$NAMESPACE" run "$1" --image=busybox:1.36 --restart=Never --labels="$2" \
    --overrides='{"spec":{"securityContext":{"runAsNonRoot":true,"runAsUser":65534,"seccompProfile":{"type":"RuntimeDefault"}},"containers":[{"name":"'"$1"'","image":"busybox:1.36","command":["sleep","300"],"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]}}}]}}' >/dev/null
  kubectl -n "$NAMESPACE" wait --for=condition=Ready "pod/$1" --timeout=60s >/dev/null
}
reach() { kubectl -n "$NAMESPACE" exec "$1" -- nc -z -w 4 "$2" "$3" >/dev/null 2>&1 && echo open || echo blocked; }
gw="hardening-gw-$RANDOM" none="hardening-none-$RANDOM"
probe "$gw" app=gateway
probe "$none" hardening=probe

declare -A want=(
  ["$gw $RELEASE_NAME-postgres 5432"]=open
  ["$gw $RELEASE_NAME-currency-mcp 8080"]=open
  ["$gw $RELEASE_NAME-dashboard 8080"]=blocked
  ["$gw 169.254.169.254 80"]=blocked
  ["$none $RELEASE_NAME-postgres 5432"]=blocked
  ["$none $RELEASE_NAME-gateway 8080"]=blocked
)
for key in "${!want[@]}"; do
  read -r pod host port <<<"$key"
  got=$(reach "$pod" "$host" "$port")
  [[ $got == "${want[$key]}" ]] || fail "$pod to $host:$port is $got, want ${want[$key]}"
  pass "$pod to $host:$port is $got"
done

log "all hardening checks passed"
