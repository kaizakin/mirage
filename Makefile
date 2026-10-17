SHELL := bash
.SHELLFLAGS := -euo pipefail -c

RELEASE_NAME ?= mirage
NAMESPACE ?= mirage
CHART_PATH ?= charts/platform
# Created by the setup hook, so Helm does not own it; must match store.setup.credentialsSecret
CREDENTIALS_SECRET ?= woo-api-credentials
LIVE_PORT ?= 8081
STORE_PORT ?= 15432
# Go services built from cmd/<name> with build/<name>.Dockerfile into image mirage/<name>.
SERVICES := woo-mcp currency-mcp gateway
IMAGE_TAG ?= dev
K3D_CLUSTER ?= mirage
export NAMESPACE RELEASE_NAME CREDENTIALS_SECRET LIVE_PORT STORE_PORT

.PHONY: help build images helm-install helm-upgrade helm-uninstall clean-state status test-live test-store e2e

help:
	@echo "Usage:"
	@echo "  make build           Compile every Go package"
	@echo "  make images          Build the $(SERVICES) images and import them into k3d cluster $(K3D_CLUSTER)"
	@echo "  make helm-install    Build images and install the platform chart (release: $(RELEASE_NAME), ns: $(NAMESPACE))"
	@echo "  make helm-upgrade    Build images and upgrade or install the platform chart"
	@echo "  make helm-uninstall  Uninstall the release and delete its volumes and API credentials"
	@echo "  make test-live       Run the WooCommerce client tests against the live store"
	@echo "  make test-store      Run the migration tests against the cluster Postgres"
	@echo "  make e2e             Run the gateway failure drills against the deployed platform"
	@echo "  make status          Check status of the release and pods"

build:
	go build ./...

images:
	for s in $(SERVICES); do docker build -f build/$$s.Dockerfile -t mirage/$$s:$(IMAGE_TAG) .; done
# One image per import: k3d reported success importing several at once yet dropped one.
	for s in $(SERVICES); do k3d image import mirage/$$s:$(IMAGE_TAG) -c $(K3D_CLUSTER); done

# A changed image ID rolls the pod even though the tag stays the same.
IMAGE_ID_FLAGS = $(foreach s,$(SERVICES),--set $(s).imageId=$$(docker image inspect -f '{{.Id}}' mirage/$(s):$(IMAGE_TAG)))

helm-install: images
	helm install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		$(IMAGE_ID_FLAGS)

helm-upgrade: images
	helm upgrade --install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace \
		$(IMAGE_ID_FLAGS)

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

test-store:
	test/e2e/store-live.sh

e2e:
	test/e2e/drills.sh
