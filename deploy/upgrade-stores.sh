#!/usr/bin/env bash
# Upgrades provisioned stores to the store chart in this checkout. The provisioner installs each
# store once, as release "store" in namespace store-<slug>, and never upgrades it; this script
# does, one store at a time, and stops at the first failure. See docs/upgrade-rollback.md.
#
#   deploy/upgrade-stores.sh [--dry-run] [--chart DIR] [SLUG] [-- HELM_ARGS...]
#
#   --dry-run   render each upgrade against the API server and change nothing
#   --chart     the store chart (default: charts/store next to this script)
#   SLUG        upgrade only store-<SLUG>; otherwise every store namespace
#   HELM_ARGS   passed to helm upgrade, such as --set woo-mcp.image.tag=0.2.0
#
# Each store keeps the values it was installed with (--reset-then-reuse-values): new chart
# defaults apply, and its host, title, and generated secrets stay. Roll one back with
#   helm rollback store <REVISION> -n store-<slug>
set -euo pipefail

usage() { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }

chart="$(cd "$(dirname "$0")/.." && pwd)/charts/store"
dry_run=false
slug=""
helm_args=()
while (($#)); do
  case "$1" in
    --dry-run) dry_run=true ;;
    --chart) chart="${2:?--chart needs a directory}"; shift ;;
    --) shift; helm_args=("$@"); break ;;
    -h | --help) usage ;;
    -*) echo "unknown flag $1" >&2; usage ;;
    *) [[ -z "$slug" ]] || usage; slug="$1" ;;
  esac
  shift
done
[[ -f "$chart/Chart.yaml" ]] || { echo "no chart at $chart" >&2; exit 2; }

if [[ -n "$slug" ]]; then
  namespaces=("store-$slug")
else
  mapfile -t namespaces < <(kubectl get namespaces -l app.kubernetes.io/managed-by=mirage-provisioner \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | sort)
fi
if ((${#namespaces[@]} == 0)); then
  echo "no stores to upgrade"
  exit 0
fi

# A store the provisioner is still creating must be left alone: it reads any release that is
# not "deployed" as an install that died, and reinstalls it.
ready() {
  local ns="$1" status available
  status=$(helm status store -n "$ns" 2>/dev/null | awk '/^STATUS:/ {print $2}')
  available=$(kubectl -n "$ns" get deployment store-woo-mcp -o jsonpath='{.status.availableReplicas}' 2>/dev/null || true)
  [[ "$status" == deployed && "${available:-0}" -ge 1 ]]
}

upgraded=() skipped=()
for ns in "${namespaces[@]}"; do
  if ! ready "$ns"; then
    if [[ -n "$slug" ]]; then
      echo "$ns is not a ready store (release not deployed, or its MCP server is not available)" >&2
      exit 1
    fi
    echo "== $ns: skipped, not ready"
    skipped+=("$ns")
    continue
  fi
  echo "== $ns"
  # setup.hook=true reruns the idempotent setup Job after the upgrade and waits for it. As a
  # plain Job, which is how the provisioner installs it, its pod template is immutable and any
  # change to it would fail the upgrade.
  args=(upgrade store "$chart" -n "$ns" --reset-then-reuse-values --set shop.setup.hook=true
    --wait --rollback-on-failure --timeout 15m "${helm_args[@]}")
  if $dry_run; then
    manifest=$(mktemp -t "upgrade-$ns.XXXXXX.yaml")
    helm "${args[@]}" --dry-run=server --hide-secret >"$manifest"
    echo "rendered without changes; manifest in $manifest"
  else
    helm "${args[@]}"
  fi
  helm history store -n "$ns" --max 5
  upgraded+=("$ns")
done

echo
echo "$($dry_run && echo "checked" || echo "upgraded"): ${upgraded[*]:-none}"
if ((${#skipped[@]})); then
  echo "skipped, not ready: ${skipped[*]}"
fi
