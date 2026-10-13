#!/bin/sh
# Publishes the application password wp-setup.sh left in /shared/pass as the CREDENTIALS_SECRET Secret.
#
# Env: SITE_URL, ADMIN_USER, CREDENTIALS_SECRET.
set -eu

if [ ! -s /shared/pass ]; then
  echo "no new application password; nothing to publish"
  exit 0
fi
kubectl create secret generic "$CREDENTIALS_SECRET" \
  --from-literal=url="$SITE_URL" \
  --from-literal=username="$ADMIN_USER" \
  --from-file=password=/shared/pass \
  --dry-run=client -o yaml | kubectl apply -f -
