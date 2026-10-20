#!/usr/bin/env bash
# Moves the named product to the trash, so test runs do not leave products in the shop.
# Orders that include it keep their line items.
#
# Usage: trash-product.sh <product name>
# Env: NAMESPACE, RELEASE_NAME, CREDENTIALS_SECRET (default woo-api-credentials).
set -euo pipefail
source "$(dirname "$0")/store-api.bash"

id=$(product_id "$1")
[[ -n $id ]] || { echo "no product named $1" >&2; exit 1; }
store_api DELETE "/products/$id" >/dev/null
