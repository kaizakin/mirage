# Deploying Mirage on one VPS

This runbook puts the platform on a single k3s node with HTTPS from Let's Encrypt. It uses the same charts as the local k3d cluster. `charts/platform/values-prod.yaml` holds every difference, and [What changes from local](#what-changes-from-local) lists them.

The examples use an Azure VM with 4 GB of RAM and the domain `mirage.kaizakin.site`:

| Host | Serves |
| --- | --- |
| `mirage.kaizakin.site` | The dashboard and its `/api` |
| `mcp.mirage.kaizakin.site` | The MCP endpoint `/mcp`, OAuth discovery, and `/oauth` |
| `<slug>.shop.mirage.kaizakin.site` | Each provisioned store |
| `grafana.mirage.kaizakin.site` | Grafana, if you install monitoring |

The dashboard and the MCP endpoint need separate hosts. The dashboard's consent page is `/oauth/authorize`, and the gateway's ingress routes the whole `/oauth` prefix to the gateway.

For a different domain, change every host in `charts/platform/values-prod.yaml` and `deploy/monitoring/values-prod.yaml`.

## 1. Create the VM

1. Create an Ubuntu 24.04 LTS VM of size `Standard_B2s` (2 vCPU, 4 GiB). Give it a 64 GiB disk.
2. Give it a static public IP address.
3. In its network security group, allow inbound TCP 22 from your own address only. Allow inbound TCP 80 and 443 from anywhere. Let's Encrypt checks each certificate over port 80. Do not open 6443: run `kubectl` and `helm` on the VM.

## 2. Point DNS at the VM

Create these A records, all with the VM's public IP:

| Name | Covers |
| --- | --- |
| `mirage.kaizakin.site` | The dashboard |
| `*.mirage.kaizakin.site` | `mcp`, `grafana` |
| `*.shop.mirage.kaizakin.site` | The stores |

Wait until each name resolves before you install anything. A certificate request for a name that does not resolve fails, and Let's Encrypt limits repeated failures.

```sh
dig +short mirage.kaizakin.site mcp.mirage.kaizakin.site anything.shop.mirage.kaizakin.site
```

## 3. Install k3s and Helm

On the VM:

```sh
curl -sfL https://get.k3s.io | INSTALL_K3S_CHANNEL=stable sh -
mkdir -p ~/.kube
sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config
sudo chown "$USER" ~/.kube/config
kubectl get nodes

curl -fsSL https://get.helm.sh/helm-v4.2.4-linux-amd64.tar.gz | tar -xz linux-amd64/helm
sudo install linux-amd64/helm /usr/local/bin/helm
```

k3s brings Traefik as the ingress controller, `local-path` as the default storage class, and a NetworkPolicy controller. `deploy/upgrade-stores.sh` needs Helm 4.

Copy the repository to the VM at the revision you deploy, for example with `git clone` and `git checkout`.

## 4. Install cert-manager and the issuers

```sh
helm repo add jetstack https://charts.jetstack.io --force-update
helm upgrade --install cert-manager jetstack/cert-manager \
  --version v1.19.1 \
  --namespace cert-manager --create-namespace \
  --set crds.enabled=true \
  --wait
kubectl apply -f deploy/vps/cluster-issuer.yaml
kubectl get clusterissuers
```

Both issuers should report `READY True`. `values-prod.yaml` uses `letsencrypt`. To test DNS and the firewall first, change each `clusterIssuer` in it to `letsencrypt-staging`. Staging certificates are not trusted by browsers, but its limits are far higher.

## 5. Get the images onto the node

Pick one way.

**From a registry.** On your workstation, sign in to ghcr.io with a token that can write packages, then build and push every image:

```sh
make push-images REGISTRY=ghcr.io/kaizakin TAG=0.1.0
```

Make each `mirage-*` package public on GitHub. The charts set no image pull secret, and every store namespace would need one.

**Without a registry.** Build on your workstation, retag, and import into k3s over SSH:

```sh
make build-images
for s in woo-mcp currency-mcp gateway provisioner shop-setup dashboard; do
  docker tag mirage/$s:dev ghcr.io/kaizakin/mirage-$s:0.1.0
done
docker save $(for s in woo-mcp currency-mcp gateway provisioner shop-setup dashboard; do echo ghcr.io/kaizakin/mirage-$s:0.1.0; done) \
  | ssh <vm> 'sudo k3s ctr images import -'
```

The names match `values-prod.yaml`, and `pullPolicy: IfNotPresent` uses the imported copies.

Build for the VM's architecture. On an Apple silicon Mac, set `DOCKER_DEFAULT_PLATFORM=linux/amd64` before you build.

## 6. Create the secrets

The chart generates the admin password, the API key, the session key, the secretbox key, and every store's passwords. You create only the chat model's key:

```sh
kubectl create namespace mirage
kubectl -n mirage create secret generic mirage-groq --from-literal=api-key="<your Groq API key>"
```

Without it, the dashboard works and chat answers 503.

## 7. Install the platform

```sh
helm upgrade --install mirage charts/platform \
  --namespace mirage \
  -f charts/platform/values-prod.yaml \
  --wait --timeout 10m
```

Read the admin password, then save the gateway Secret somewhere safe. Its `secretbox-key` seals every stored upstream credential. If you lose it, those credentials cannot be read.

```sh
kubectl -n mirage get secret mirage-gateway -o jsonpath='{.data.admin-password}' | base64 -d; echo
kubectl -n mirage get secret mirage-gateway -o yaml > mirage-gateway-secret.yaml   # keep this off the VM
```

Sign in at `https://mirage.kaizakin.site` as `admin@mirage.local`.

## 8. Verify

Certificates. Each Ingress gets its own. The gateway's certificate covers both hosts, because `/api` on the dashboard host routes to the gateway.

```sh
kubectl get certificates -A        # READY True for mirage-gateway-tls and mirage-dashboard-tls
kubectl get challenges -A          # empty once issuance is done; describe one if it is stuck
```

The MCP endpoint rejects a request without credentials and points to its OAuth metadata:

```sh
curl -si -X POST https://mcp.mirage.kaizakin.site/mcp -H 'Content-Type: application/json' -d '{}' \
  | grep -iE '^HTTP|^www-authenticate'
# HTTP/2 401
# www-authenticate: Bearer resource_metadata="https://mcp.mirage.kaizakin.site/.well-known/oauth-protected-resource"
```

OAuth discovery returns the issuer on the MCP host:

```sh
curl -s https://mcp.mirage.kaizakin.site/.well-known/oauth-protected-resource
curl -s https://mcp.mirage.kaizakin.site/.well-known/oauth-authorization-server
```

The dashboard loads over HTTPS:

```sh
curl -sI https://mirage.kaizakin.site | head -1    # HTTP/2 200
```

Create a store on the dashboard's Stores page. It becomes ready in a few minutes. Its certificate arrives about a minute after its Ingress is created; until then Traefik serves its own untrusted default certificate. Then:

```sh
kubectl get certificates -n store-<slug>
curl -sI https://<slug>.shop.mirage.kaizakin.site | head -1
```

NetworkPolicies are enforced. k3s runs kube-router's policy controller, which writes one `KUBE-POD-FW` chain per selected pod. A count of zero means policies are not enforced:

```sh
sudo iptables-save | grep -c KUBE-POD-FW
```

A pod outside the store must not reach the store's MySQL. This command should time out:

```sh
kubectl run np-check --rm -it --restart=Never --image=busybox:1.36 -- \
  nc -zv -w 3 store-mysql.store-<slug>.svc.cluster.local 3306
```

## 9. Monitoring (optional)

Prometheus and Grafana add about 0.5 GiB of memory requests. On a 4 GB node they fit beside the platform and one or two stores. Skip them if you need room for more stores.

To install them, create Grafana's admin Secret first. The chart's built-in default password is public.

```sh
kubectl create namespace monitoring
kubectl -n monitoring create secret generic grafana-admin \
  --from-literal=admin-user=admin --from-literal=admin-password="$(openssl rand -base64 24)"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update
helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
  --version 92.13.1 \
  --namespace monitoring \
  -f deploy/monitoring/values.yaml -f deploy/monitoring/values-prod.yaml \
  --wait --timeout 10m
helm upgrade mirage charts/platform -n mirage -f charts/platform/values-prod.yaml --wait
```

The last command adds the gateway's ServiceMonitor and the Mirage dashboard, which need the CRDs the first one installed. Grafana is then at `https://grafana.mirage.kaizakin.site`.

To free the memory later, run `helm uninstall monitoring -n monitoring`.

## 10. Memory on 4 GB

`kubectl top node` shows what is in use. One store requests about 0.6 GiB and may use up to 1.8 GiB, including its setup Job. `maxPerUser: 1` caps stores per user, not in total, so the number of members decides how many stores can exist. When a new store stays pending with `Insufficient memory`, delete a store or uninstall monitoring.

## What changes from local

| Setting | Local cluster (`values.yaml`) | VPS (`values-prod.yaml`) |
| --- | --- | --- |
| Dashboard host | `dash.127.0.0.1.nip.io` | `mirage.kaizakin.site` |
| MCP and OAuth host | `gateway.127.0.0.1.nip.io` | `mcp.mirage.kaizakin.site` |
| Store domain (`gateway.stores.domain`) | `shop.127.0.0.1.nip.io` | `shop.mirage.kaizakin.site` |
| Gateway and dashboard scheme (`gateway.scheme`) | `http` | `https` |
| Store scheme (`gateway.stores.scheme`, `provisioner.stores.scheme`) | `http` | `https` |
| Secure session cookie (`gateway.secureCookies`) | `false` | `true` |
| TLS (`gateway.ingress.tls`, `dashboard.ingress.tls`) | off | on, issuer `letsencrypt` |
| Store TLS (`provisioner.stores.values.shop.wordpress.ingress.tls`) | off | on, issuer `letsencrypt` |
| Admin password (`gateway.admin.password`) | `mirage-local-admin` | empty, so generated |
| Platform images | `mirage/<name>:dev`, imported by `make images` | `ghcr.io/kaizakin/mirage-<name>:<tag>` |
| Store images (`provisioner.stores.values`) | `mirage/woo-mcp:dev`, `mirage/shop-setup:dev` | `ghcr.io/kaizakin/mirage-woo-mcp:<tag>`, `ghcr.io/kaizakin/mirage-shop-setup:<tag>` |
| Image rollout | `imageId` annotation changes with each local build | a new tag per release |
| Storage class | default (`local-path` in k3d) | default (`local-path` in k3s) |
| Postgres volume | 5 GiB | 2 GiB |
| Store volumes (WordPress, MySQL) | 4 GiB each | 2 GiB each |
| Store quota, requests | 1 CPU, 1536 MiB | 0.5 CPU, 768 MiB |
| Store quota, limits | 4 CPU, 3 GiB | 4 CPU, 2 GiB |
| MySQL | image defaults | 64 MiB buffer pool, performance schema off, 256 MiB request |
| WordPress request | 200m CPU, 256 MiB | 100m CPU, 128 MiB |
| Gateway | 2 replicas, 64 MiB request | 2 replicas, 32 MiB request |
| Stores per user (`gateway.stores.maxPerUser`) | 3 | 1 |
| Generated secrets | chart-generated, kept across upgrades | the same; back up `mirage-gateway` |
| Chat key | `mirage-groq` Secret, created by hand | the same |
| Grafana | `grafana.127.0.0.1.nip.io`, HTTP, chart default password | `grafana.mirage.kaizakin.site`, TLS, `grafana-admin` Secret |
| Prometheus retention | 3 days | 1 day |

## Upgrades and rollback

See [docs/upgrade-rollback.md](../../docs/upgrade-rollback.md). It covers `helm upgrade`, `helm history`, and `helm rollback` for the platform, what migrations mean for a rollback, and `deploy/upgrade-stores.sh` for the stores.
