SHELL := bash
.SHELLFLAGS := -euo pipefail -c

RELEASE_NAME ?= mirage
NAMESPACE ?= mirage
CHART_PATH ?= charts/platform
# Created by the setup hook, so Helm does not own it; must match store.setup.credentialsSecret
CREDENTIALS_SECRET ?= woo-api-credentials
LIVE_PORT ?= 8081
export NAMESPACE RELEASE_NAME CREDENTIALS_SECRET LIVE_PORT

.PHONY: help install-wordpress upgrade-wordpress uninstall-wordpress clean-wordpress-state status-wordpress test-live

help:
	@echo "Usage:"
	@echo "  make install-wordpress    Install WordPress chart (release: $(RELEASE_NAME), ns: $(NAMESPACE))"
	@echo "  make upgrade-wordpress    Upgrade or install WordPress chart"
	@echo "  make uninstall-wordpress  Uninstall the release and delete its volumes and API credentials"
	@echo "  make test-live            Run the WooCommerce client tests against the live store"
	@echo "  make status-wordpress     Check status of the release and pods"

install-wordpress:
	helm install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace

upgrade-wordpress:
	helm upgrade --install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace

# helm uninstall keeps StatefulSet volumes and the hook-created Secret. A reinstall would then reuse a MySQL
# data dir initialized with the old password, so every uninstall wipes them.
clean-wordpress-state:
	kubectl -n $(NAMESPACE) delete secret $(CREDENTIALS_SECRET) --ignore-not-found
# --wait=false: volumes stay protected until uninstall stops the pods using them
	kubectl -n $(NAMESPACE) delete pvc -l app.kubernetes.io/instance=$(RELEASE_NAME) --wait=false

uninstall-wordpress: clean-wordpress-state
	helm uninstall $(RELEASE_NAME) --namespace $(NAMESPACE) --wait
# Block until the volumes are gone, or an immediate reinstall binds to a PVC that is still terminating
	kubectl -n $(NAMESPACE) wait --for=delete pvc -l app.kubernetes.io/instance=$(RELEASE_NAME) --timeout=180s

status-wordpress:
	@echo "=== Helm Release Status ==="
	helm status $(RELEASE_NAME) --namespace $(NAMESPACE)
	@printf "\n=== Pods in Namespace %s ===\n" "$(NAMESPACE)"
	kubectl get pods -n $(NAMESPACE)

test-live:
	test/e2e/woo-live.sh
