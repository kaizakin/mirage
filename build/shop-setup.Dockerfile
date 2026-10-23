# The store setup Job's image: wp-cli plus the WooCommerce plugin and the store theme at pinned
# versions, so provisioning a store downloads nothing from wordpress.org. Every store fetching
# 23 MB at setup made provisioning as slow and as flaky as that download.
# Build context is the repo root: docker build -f build/shop-setup.Dockerfile .
FROM wordpress:cli-2.12.0
# World-readable: the Job runs as uid 33, not this image's www-data (uid 82). The directories
# come first, since ADD would give the ones it creates the files' mode.
USER root
RUN install -d -m 755 /opt/mirage/plugins /opt/mirage/themes
ADD --chmod=644 --checksum=sha256:5317434cb381b5fdc7c1dd6ee258324f08c390976aaf8d1199f2a0e9a3ab8586 \
    https://downloads.wordpress.org/plugin/woocommerce.11.2.1.zip /opt/mirage/plugins/woocommerce.zip
ADD --chmod=644 --checksum=sha256:10c42f4e58f12acfbac9bf57c0591e448dd64c3edd771bd938389388a9e58352 \
    https://downloads.wordpress.org/theme/extendable.2.1.9.zip /opt/mirage/themes/extendable.zip
USER www-data
