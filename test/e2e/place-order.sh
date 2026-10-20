#!/usr/bin/env bash
# Places a cash-on-delivery order for one unit of the named product, as a storefront
# checkout would, and prints the order id.
#
# Usage: place-order.sh <product name>
# Env: NAMESPACE, RELEASE_NAME, CREDENTIALS_SECRET (default woo-api-credentials).
set -euo pipefail
source "$(dirname "$0")/store-api.bash"

id=$(product_id "$1")
[[ -n $id ]] || { echo "no product named $1" >&2; exit 1; }
store_api POST /orders "$(jq -n --argjson id "$id" '{status: "processing", payment_method: "cod", payment_method_title: "Cash on delivery",
  billing: {first_name: "Ada", last_name: "Buyer", email: "ada.buyer@example.com"},
  line_items: [{product_id: $id, quantity: 1}]}')" | jq -r .id
