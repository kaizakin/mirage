#!/bin/sh
# Installs WordPress + WooCommerce on the shared volume and, unless CREDENTIALS_SECRET already exists,
# writes a fresh application password to /shared/pass. Safe to rerun on every install and upgrade.
#
# Env: SITE_URL, ADMIN_USER, ADMIN_PASS, CREDENTIALS_SECRET, WORDPRESS_DB_* (read by wp-config.php).
set -eu

until [ -f wp-config.php ]; do
  echo "waiting for the WordPress pod to populate the volume"
  sleep 3
done

# mysqli rather than `wp db`: the image's MariaDB client can't auth against MySQL 8's caching_sha2_password.
# shellcheck disable=SC2016 # $h and $p are PHP variables
until php -r 'mysqli_report(MYSQLI_REPORT_OFF);
  [$h, $p] = array_pad(explode(":", getenv("WORDPRESS_DB_HOST")), 2, 3306);
  exit(@mysqli_connect($h, getenv("WORDPRESS_DB_USER"), getenv("WORDPRESS_DB_PASSWORD"), getenv("WORDPRESS_DB_NAME"), (int) $p) ? 0 : 1);'; do
  echo "waiting for the database"
  sleep 3
done

if ! wp core is-installed; then
  wp core install --url="$SITE_URL" --title=Shop --admin_user="$ADMIN_USER" \
    --admin_password="$ADMIN_PASS" --admin_email=admin@example.com --skip-email
fi
# A site installed before this hook existed keeps its own administrators; don't invent one.
if ! wp user get "$ADMIN_USER" --field=ID >/dev/null 2>&1; then
  echo "WordPress has no user '$ADMIN_USER'; set store.setup.adminUser to an existing administrator" >&2
  exit 1
fi
# core install only sets the URL once; keep it in sync with values on every upgrade.
wp option update home "$SITE_URL"
wp option update siteurl "$SITE_URL"

wp plugin is-installed woocommerce || wp plugin install woocommerce
wp plugin is-active woocommerce || wp plugin activate woocommerce
wp rewrite structure '/%postname%/'
# New WooCommerce stores default to a "coming soon" page for logged-out visitors; publish the storefront.
wp option update woocommerce_coming_soon no
wp option update show_on_front page
wp option update page_on_front "$(wp option get woocommerce_shop_page_id)"
wp wc payment_gateway update cod --enabled=true --user="$ADMIN_USER"
if [ -z "$(wp post list --post_type=product --name=seed-mug --post_status=any --format=ids)" ]; then
  wp wc product create --name="Seed Mug" --slug=seed-mug --regular_price=299 --user="$ADMIN_USER"
fi

if [ -s /existing/password ]; then
  echo "$CREDENTIALS_SECRET already exists; leaving the application password alone"
  exit 0
fi
# The plaintext of any earlier "mcp" password is lost (a previous run died before saving it), so revoke it.
# WordPress also rejects a duplicate name.
for uuid in $(wp user application-password list "$ADMIN_USER" --name=mcp --field=uuid); do
  wp user application-password delete "$ADMIN_USER" "$uuid"
done
wp user application-password create "$ADMIN_USER" mcp --porcelain >/shared/pass
