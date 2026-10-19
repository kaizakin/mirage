RELEASE_NAME ?= mirage
NAMESPACE ?= mirage
CHART_PATH ?= charts/wordpress

.PHONY: help install-wordpress upgrade-wordpress uninstall-wordpress status-wordpress

help:
	@echo "Usage:"
	@echo "  make install-wordpress    Install WordPress chart (release: $(RELEASE_NAME), ns: $(NAMESPACE))"
	@echo "  make upgrade-wordpress    Upgrade or install WordPress chart"
	@echo "  make uninstall-wordpress  Uninstall the WordPress release"
	@echo "  make status-wordpress     Check status of the release and pods"

install-wordpress:
	helm install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace

upgrade-wordpress:
	helm upgrade --install $(RELEASE_NAME) $(CHART_PATH) \
		--namespace $(NAMESPACE) \
		--create-namespace

uninstall-wordpress:
	helm uninstall $(RELEASE_NAME) --namespace $(NAMESPACE)

status-wordpress:
	@echo "=== Helm Release Status ==="
	helm status $(RELEASE_NAME) --namespace $(NAMESPACE)
	@echo "\n=== Pods in Namespace $(NAMESPACE) ==="
	kubectl get pods -n $(NAMESPACE)
