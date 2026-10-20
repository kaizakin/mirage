# Sourced by scripts that call the store's WooCommerce REST API directly, with the
# application password the setup hook published. Sourcing it opens a port-forward to
# WordPress that closes when the script exits.
#
# Env: NAMESPACE, RELEASE_NAME, CREDENTIALS_SECRET (default woo-api-credentials).

store_port=18082
store_secret() { kubectl -n "$NAMESPACE" get secret "${CREDENTIALS_SECRET:-woo-api-credentials}" -o "jsonpath={.data.$1}" | base64 -d; }
store_auth="$(store_secret username):$(store_secret password)"

kubectl -n "$NAMESPACE" port-forward "svc/$RELEASE_NAME-wordpress" "$store_port:80" >/dev/null &
store_port_forward=$!
trap 'kill "$store_port_forward"' EXIT
for _ in {1..30}; do
  curl -sf -o /dev/null "http://localhost:$store_port/wp-json/" && break
  sleep 1
done

# store_api <method> <path> [json]: calls /wp-json/wc/v3<path> and prints the response.
store_api() {
  local args=(-sS -u "$store_auth" -X "$1" "http://localhost:$store_port/wp-json/wc/v3$2")
  [[ -n ${3:-} ]] && args+=(-H 'Content-Type: application/json' -d "$3")
  curl "${args[@]}"
}

# product_id <name>: prints the id of the product with exactly this name, or nothing.
product_id() {
  curl -sS -u "$store_auth" -G "http://localhost:$store_port/wp-json/wc/v3/products" --data-urlencode "search=$1" |
    jq -r --arg n "$1" 'map(select(.name == $n)) | first | .id // empty'
}
