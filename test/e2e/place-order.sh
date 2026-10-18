#!/usr/bin/env bash
# Places a cash-on-delivery order for one unit of the named product, as a storefront
# checkout would, and prints the order id. Uses the store's REST API with the application
# password the setup hook published.
#
# Usage: place-order.sh <product name>
# Env: NAMESPACE, RELEASE_NAME, CREDENTIALS_SECRET (default woo-api-credentials).
set -euo pipefail

name=$1
port=18082
secret() { kubectl -n "$NAMESPACE" get secret "${CREDENTIALS_SECRET:-woo-api-credentials}" -o "jsonpath={.data.$1}" | base64 -d; }
auth="$(secret username):$(secret password)"

kubectl -n "$NAMESPACE" port-forward "svc/$RELEASE_NAME-wordpress" "$port:80" >/dev/null &
port_forward=$!
trap 'kill "$port_forward"' EXIT
for _ in {1..30}; do
  curl -sf -o /dev/null "http://localhost:$port/wp-json/" && break
  sleep 1
done

product_id=$(curl -sS -u "$auth" -G "http://localhost:$port/wp-json/wc/v3/products" --data-urlencode "search=$name" |
  jq -r --arg n "$name" 'map(select(.name == $n)) | first | .id // empty')
[[ -n $product_id ]] || { echo "no product named $name" >&2; exit 1; }

curl -sS -u "$auth" -H 'Content-Type: application/json' -X POST "http://localhost:$port/wp-json/wc/v3/orders" \
  -d "$(jq -n --argjson id "$product_id" '{status: "processing", payment_method: "cod", payment_method_title: "Cash on delivery",
        billing: {first_name: "Ada", last_name: "Buyer", email: "ada.buyer@example.com"},
        line_items: [{product_id: $id, quantity: 1}]}')" | jq -r .id
