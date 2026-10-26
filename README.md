# Mirage

Mirage is an MCP gateway and store platform on Kubernetes. A team gets one MCP endpoint that serves the tools of every connected server, with per-person API keys or browser sign-in, tool permissions per role and per person, and one audit row for every call. Admins and members create WooCommerce stores from the dashboard. Each store runs in its own namespace, and its MCP server is registered with the gateway automatically. A chat agent in the dashboard uses the same tools as the signed-in person.

| Component | What it does | Source |
| --- | --- | --- |
| Gateway | MCP endpoint `/mcp`, dashboard API `/api`, OAuth server, chat agent, `/metrics` | `cmd/gateway`, `internal/gateway`, `internal/api`, `internal/oauth`, `internal/agent` |
| Dashboard | Servers, Health, Stores, Members, Permissions, API keys, Audit log, Chat | `web/dashboard` |
| WooCommerce MCP server | Products and orders tools for one store, run inside that store's namespace | `cmd/woo-mcp`, `internal/woomcp`, `internal/woo` |
| Provisioner | Turns store rows and hosted-server rows into Helm releases | `cmd/provisioner`, `internal/provisioner` |
| Currency MCP server | A fixed-rate converter, the second built-in upstream | `cmd/currency-mcp`, `internal/currencymcp` |
| Charts | The platform, one store, and one hosted MCP server | `charts/platform`, `charts/store`, `charts/hosted-mcp` |

[docs/architecture.md](docs/architecture.md) explains the system design and tradeoffs, and [docs/threat-model.md](docs/threat-model.md) explains the threat model.

## Local setup

You need Docker, k3d, kubectl, Helm 4, Go 1.26, Node 24, jq, and curl.

1. Create a single-node cluster with ports 80 and 443 mapped to the host:

   ```bash
   k3d cluster create mirage -p "80:80@loadbalancer" -p "443:443@loadbalancer"
   ```

2. Add the chat model's key. Groq's free tier works:

   ```bash
   kubectl create namespace mirage
   kubectl -n mirage create secret generic mirage-groq --from-literal=api-key="<your Groq API key>"
   ```

3. Build every image, import it into k3d, and install the platform:

   ```bash
   make helm-install
   ```

   The platform is up in about a minute. `make helm-upgrade` rebuilds and upgrades after a change.

4. Open the dashboard at `http://dash.127.0.0.1.nip.io` and sign in as `admin@mirage.local` with password `mirage-local-admin`.

5. Optional monitoring. This installs Prometheus and Grafana, and Grafana is then at `http://grafana.127.0.0.1.nip.io` with a Mirage dashboard:

   ```bash
   make monitoring
   ```

The hosts use nip.io, so they resolve to 127.0.0.1 with nothing to configure. `make help` lists every target.

## VPS setup with k3s

The VPS runs the same charts. `charts/platform/values-prod.yaml` holds every difference: real hosts, HTTPS through cert-manager and Let's Encrypt, secure cookies, generated passwords, registry images, and a resource profile sized for a 4 GB VM. The full runbook is [deploy/vps/README.md](deploy/vps/README.md). In short:

1. Create the VM with a static IP, and open ports 80 and 443.
2. Point DNS at it. The A records are `mirage.<domain>`, `*.mirage.<domain>`, and `*.shop.mirage.<domain>`.
3. Install k3s and Helm. k3s brings Traefik, the `local-path` storage class, and a NetworkPolicy controller.
4. Install cert-manager, then apply the issuers:

   ```bash
   kubectl apply -f deploy/vps/cluster-issuer.yaml
   ```

5. Push the images to a registry:

   ```bash
   make push-images REGISTRY=ghcr.io/<owner> TAG=0.1.0
   ```

6. Create the `mirage-groq` Secret, then install:

   ```bash
   helm upgrade --install mirage charts/platform -n mirage -f charts/platform/values-prod.yaml --wait
   ```

7. Read the generated admin password, and back up the `mirage-gateway` Secret. Its `secretbox-key` decrypts every stored upstream credential.

The runbook's table lists every value that changes between the local cluster and the VPS. [docs/upgrade-rollback.md](docs/upgrade-rollback.md) covers upgrades and rollback of the platform, the stores, and hosted MCP servers.

## Using it

### Create a store

On the Stores page, enter a name and choose Create. The store is ready in about two minutes at `http://<slug>.shop.127.0.0.1.nip.io`. It comes with a catalog, and its tools appear in the gateway as `store-<slug>__<tool>`. Members may create up to three stores each.

The store's WordPress admin is at `/wp-admin` as user `admin`. Its password is in the store's namespace:

```bash
kubectl -n store-<slug> get secret store-wordpress-admin -o jsonpath='{.data.admin-password}' | base64 -d; echo
```

### Place an order

Open the storefront, add a product to the cart, and check out with cash on delivery. Checkout creates a customer account, and My account then shows the order and its status. The order appears in the store's `list_orders` tool at once.

To place one from a script instead:

```bash
STORE=<slug> test/e2e/place-order.sh "<product name>"
```

### Connect an external MCP client

Any MCP client that speaks streamable HTTP connects to `http://gateway.127.0.0.1.nip.io/mcp`. There are two ways to authenticate.

With an API key, generate one on the API keys page. It is shown once. Then:

```bash
claude mcp add --transport http gw http://gateway.127.0.0.1.nip.io/mcp --header "Authorization: Bearer <key>"
```

With a browser sign-in, add the server without a header. The client finds the gateway's OAuth metadata, registers itself, and opens the dashboard's consent page:

```bash
claude mcp add --transport http gw http://gateway.127.0.0.1.nip.io/mcp
```

Connected apps are listed, and revocable, on the API keys page.

A client with many servers behind the gateway can ask for search mode by adding `--header "X-Mirage-Tools: search"`. Its tool list then holds `mirage__search_tools` and `mirage__call_tool`, and the model fetches only the schemas it needs.

### Use the chat agent

Open Chat in the dashboard and ask in plain language, for example "What orders came in today at tea-house?" or "Create a product named Demo Mug priced 499 in the tea-house store." Each tool call streams as a card with its arguments and result. The agent calls the gateway as the signed-in person, so it sees only that person's tools, and the Audit page records its calls under their name.

## Tests

| Command | What it proves |
| --- | --- |
| `make lint` | gofmt, go vet, staticcheck, and revive are clean |
| `go test ./...` | Unit and integration tests for every package |
| `make test-store` | Store queries and migrations against the cluster's Postgres |
| `make e2e` | Role checks, failure drills, OAuth, tool permissions, hosted servers, provisioner RBAC and default-deny networking, search mode, scaling, and store provisioning, against the running cluster |
| `test/e2e/dod.sh` | The full flow: a store, a product made through chat, an order, Claude Code, the audit log, and a revoked key |

## Bonus and stand-out work

Every bonus item is built. The scripts named below prove each one against the running cluster.

| Item | Where to find it |
| --- | --- |
| Production-like VPS deployment, with TLS from cert-manager | `charts/platform/values-prod.yaml`, `deploy/vps/README.md`, `deploy/vps/cluster-issuer.yaml`, the `ingress.tls` values in each chart |
| Tool permissions per role and per person, hidden from the tool list and refused on call | `internal/gateway/policy`, `internal/gateway/downstream/server.go`, `internal/store/toolrules.go`, the Permissions page; `test/e2e/permissions.sh` |
| OAuth 2.1 browser sign-in from the MCP authorization spec, alongside API keys | `internal/oauth`, `internal/auth/oauth.go`, the consent page `web/dashboard/src/pages/Authorize.tsx`; `test/e2e/oauth.sh` |
| Handling many tools with search-then-call tools | `internal/gateway/downstream/search.go`; `test/e2e/tool-search.sh` |
| Hosted MCP servers from a container image, with their own Secret, limits, and NetworkPolicy | `charts/hosted-mcp`, `internal/provisioner/hosted.go`, `internal/store/hosted.go`, the Servers page; `test/e2e/hosted.sh` |
| SSRF protection for admin-added URLs, checked again at connect time | `internal/gateway/ssrf`, `internal/gateway/upstream/transport.go` |
| Least-privilege RBAC with an admission policy, default-deny NetworkPolicies, non-root containers | `charts/platform/charts/provisioner/templates/rbac.yaml`, `charts/platform/templates/networkpolicy-default-deny.yaml`, each chart's `securityContext`; `test/e2e/hardening.sh` |
| Upstream credentials encrypted at rest with AES-256-GCM | `internal/secretbox`, `internal/store/servers.go` |
| Several gateway replicas with session affinity, and per-user rate limits | `internal/gateway/affinity`, `internal/gateway/ratelimit`; `test/e2e/scaling.sh` |
| Metrics per server, the Health page, and a Grafana dashboard | `internal/gateway/metrics`, `internal/api/health.go`, `charts/platform/files/grafana/mirage-gateway.json`, `deploy/monitoring` |
| Upgrades and rollback for the platform, stores, and hosted servers | `docs/upgrade-rollback.md`, `deploy/upgrade-stores.sh` |
| Stores created and deleted from the dashboard, each in its own namespace with quotas | `internal/provisioner`, `charts/store`, the Stores page; `test/e2e/stores.sh` |
