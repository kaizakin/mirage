SHELL := bash
.SHELLFLAGS := -euo pipefail -c

RELEASE_NAME ?= mirage
NAMESPACE ?= mirage
CHART_PATH ?= charts/platform
# Created by the setup hook, so Helm does not own it; must match store.setup.credentialsSecret
CREDENTIALS_SECRET ?= woo-api-credentials
LIVE_PORT ?= 8081
WOO_MCP_IMAGE ?= mirage/woo-mcp:dev
K3D_CLUSTER ?= mirage
export NAMESPACE RELEASE_NAME CREDENTIALS_SECRET LIVE_PORT

.PHONY: help build images helm-install helm-upgrade helm-uninstall clean-state status test-live

help:
	@echo "Usage:"
	@echo "  make build           Compile every Go package"
	@echo "  make images          Build $(WOO_MCP_IMAGE) and import it into k3d cluster $(K3D_CLUSTER)"
	@echo "  make helm-install    Build images and install the platform chart (release: $(RELEASE_NAME), ns: $(NAMESPACE))"
	@echo "  make helm-upgrade    Build images and upgrade or install the platform chart"
	@echo "  make helm-uninstall  Uninstall the release and delete its volumes and API credentials"
	@echo "  make test-live       Run the WooCommerce client tests against the live store"
	@echo "  make status          Check status of the release and pods"

build:
	go build ./...

images:
	docker build -f build/woo-mcp.Dockerfile -t $(WOO_MCP_IMAGE) .
	k3d image import $(WOO_MCP_IMAGE) -c $(K3D_CLUSTER)

helm-install: images
	helm install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set woo-mcp.imageId=$$(docker image inspect -f '{{.Id}}' $(WOO_MCP_IMAGE))

helm-upgrade: images
	helm upgrade --install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		--set woo-mcp.imageId=$$(docker image inspect -f '{{.Id}}' $(WOO_MCP_IMAGE))

# helm uninstall keeps StatefulSet volumes and the hook-created Secret. A reinstall would then reuse a MySQL
# data dir initialized with the old password, so every uninstall wipes them.
clean-state:
	kubectl -n $(NAMESPACE) delete secret $(CREDENTIALS_SECRET) --ignore-not-found
# --wait=false: volumes stay protected until uninstall stops the pods using them
	kubectl -n $(NAMESPACE) delete pvc -l app.kubernetes.io/instance=$(RELEASE_NAME) --wait=false

helm-uninstall: clean-state
	helm uninstall $(RELEASE_NAME) --namespace $(NAMESPACE) --wait
# Block until the volumes are gone, or an immediate reinstall binds to a PVC that is still terminating
	kubectl -n $(NAMESPACE) wait --for=delete pvc -l app.kubernetes.io/instance=$(RELEASE_NAME) --timeout=180s

status:
	@echo "=== Helm Release Status ==="
	helm status $(RELEASE_NAME) --namespace $(NAMESPACE)
	@printf "\n=== Pods in Namespace %s ===\n" "$(NAMESPACE)"
	kubectl get pods -n $(NAMESPACE)

test-live:
	test/e2e/woo-live.sh
