SHELL := bash
.SHELLFLAGS := -euo pipefail -c

RELEASE_NAME ?= mirage
NAMESPACE ?= mirage
CHART_PATH ?= charts/platform
LIVE_PORT ?= 8081
STORE_PORT ?= 15432
# Go services built from cmd/<name> with build/<name>.Dockerfile into image mirage/<name>;
# the dashboard image builds from web/dashboard. woo-mcp runs in each provisioned store.
SERVICES := woo-mcp currency-mcp gateway provisioner
# Images built from build/<name>.Dockerfile: the Go services, plus the store setup Job's wp-cli
# with WooCommerce and the theme built in.
BUILD_IMAGES := $(SERVICES) shop-setup
IMAGE_TAG ?= dev
K3D_CLUSTER ?= mirage
MONITORING_NAMESPACE ?= monitoring
KUBE_PROMETHEUS_STACK_VERSION ?= 92.13.1
# A provisioned store's slug, for the targets that test one store (test-live).
STORE ?=
# Where push-images sends the images, as REGISTRY/mirage-<name>:TAG.
REGISTRY ?=
TAG ?=
export NAMESPACE RELEASE_NAME LIVE_PORT STORE_PORT STORE

.PHONY: help build lint build-images images push-images helm-install helm-upgrade helm-uninstall clean-state status test-live test-store e2e monitoring

help:
	@echo "Usage:"
	@echo "  make build           Compile every Go package"
	@echo "  make lint            gofmt, go vet, staticcheck, and revive over the Go code"
	@echo "  make build-images    Build the $(BUILD_IMAGES) and dashboard images as mirage/<name>:$(IMAGE_TAG)"
	@echo "  make images          Build the images and import them into k3d cluster $(K3D_CLUSTER)"
	@echo "  make push-images     Build the images and push them as REGISTRY/mirage-<name>:TAG"
	@echo "  make helm-install    Build images and install the platform chart (release: $(RELEASE_NAME), ns: $(NAMESPACE))"
	@echo "  make helm-upgrade    Build images and upgrade or install the platform chart"
	@echo "  make helm-uninstall  Uninstall the release and delete its volumes and every provisioned store"
	@echo "  make test-live       Run the WooCommerce client tests against store STORE=<slug>"
	@echo "  make test-store      Run the migration tests against the cluster Postgres"
	@echo "  make e2e             Run every end-to-end check against the running cluster"
	@echo "  make status          Check status of the release and pods"
	@echo "  make monitoring      Install Prometheus and Grafana, then upgrade the platform so it is scraped"

build:
	go build ./...

# Pinned so lint results do not change under us; revive walks the tree itself, so it is told
# to skip the dashboard's node_modules, which can contain stray .go files.
lint:
	test -z "$$(gofmt -l $$(go list -f '{{.Dir}}' ./...))" || { gofmt -l $$(go list -f '{{.Dir}}' ./...); exit 1; }
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 -checks all ./...
	go run github.com/mgechev/revive@v1.17.1 -formatter plain -set_exit_status -exclude ./web/... ./...

build-images:
	for s in $(BUILD_IMAGES); do docker build -f build/$$s.Dockerfile -t mirage/$$s:$(IMAGE_TAG) .; done
	docker build -t mirage/dashboard:$(IMAGE_TAG) web/dashboard

images: build-images
# One image per import: k3d reported success importing several at once yet dropped one.
	for s in $(BUILD_IMAGES) dashboard; do k3d image import mirage/$$s:$(IMAGE_TAG) -c $(K3D_CLUSTER); done

# For a cluster that pulls from a registry, such as the VPS (deploy/vps/README.md):
#   make push-images REGISTRY=ghcr.io/<owner> TAG=0.1.0
# Give every release a new TAG: nodes keep pulled images by tag and would not fetch a reused one.
push-images:
	test -n "$(REGISTRY)" && test -n "$(TAG)" || { echo "usage: make push-images REGISTRY=ghcr.io/<owner> TAG=<version>" >&2; exit 1; }
	$(MAKE) build-images
	for s in $(BUILD_IMAGES) dashboard; do \
		docker tag mirage/$$s:$(IMAGE_TAG) $(REGISTRY)/mirage-$$s:$(TAG); \
		docker push $(REGISTRY)/mirage-$$s:$(TAG); \
	done

# A changed image ID rolls the pod even though the tag stays the same. Stores' woo-mcp pods
# take the image as it is when the store is provisioned.
CHART_IMAGES := currency-mcp gateway provisioner dashboard
IMAGE_ID_FLAGS = $(foreach s,$(CHART_IMAGES),--set $(s).imageId=$$(docker image inspect -f '{{.Id}}' mirage/$(s):$(IMAGE_TAG)))

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

# helm uninstall keeps StatefulSet volumes, and a reinstall would then reuse a Postgres data dir
# initialized with the old password, so every uninstall wipes them. Stores go too: without the
# provisioner nothing would ever remove their namespaces.
clean-state:
	kubectl delete namespace -l app.kubernetes.io/managed-by=mirage-provisioner --wait=false
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
	test/e2e/api-roles.sh
	test/e2e/drills.sh
	test/e2e/oauth.sh
	test/e2e/permissions.sh
	test/e2e/hosted.sh
	test/e2e/hardening.sh
	test/e2e/tool-search.sh
	test/e2e/scaling.sh
	test/e2e/stores.sh

# Prometheus and Grafana (kube-prometheus-stack) in their own namespace. The platform is
# upgraded afterwards: its ServiceMonitor and Grafana dashboard need the operator's CRDs.
monitoring:
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
	helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
		--version $(KUBE_PROMETHEUS_STACK_VERSION) \
		--namespace $(MONITORING_NAMESPACE) \
		--create-namespace \
		--values deploy/monitoring/values.yaml \
		--wait --timeout 10m
	$(MAKE) helm-upgrade
