# System design and tradeoffs

[tradeoffs.md](tradeoffs.md) records each design decision in detail, with the alternative and when to revisit it.

## Architecture

```text
 MCP clients ──┐                         ┌── currency-mcp            (namespace mirage)
 browser ──────┼── Traefik ── gateway ×2 ┼── store-<slug> woo-mcp   (namespace store-<slug>, one per store)
 shoppers ─────┘      │          │       ├── hosted servers         (namespace mcp-<slug>, one per image)
                      │          │       └── public MCP servers     (DeepWiki, and any an admin adds)
                      │       Postgres
                      └── WordPress of each store
                                 ▲
                      provisioner (reconciles store and hosted rows into Helm releases)
```

- **One gateway binary.** It serves the MCP endpoint `/mcp`, the dashboard's REST API `/api`, the OAuth authorization server, the chat agent, and `/metrics`. Two replicas run behind Traefik. Each MCP session ID names the replica that owns it, under an HMAC, and a replica that receives another replica's session forwards the request there. Sessions therefore survive a load balancer without sticky routing.
- **Postgres holds all state.** It stores users, sessions, hashed API keys and OAuth tokens, servers and their sealed credentials, tool rules, stores, hosted servers, rate-limit windows, and the audit log. The gateway runs embedded migrations on startup under a lock. Every migration adds and never removes, so an older gateway runs on a newer schema.
- **Stores and hosted servers are desired state in Postgres.** The provisioner is a level-triggered reconciler. Every few seconds it compares each row with the cluster and takes the next step: create the namespace, install the Helm release, wait for health, then register the MCP server or mark it failed with a reason. Nothing lives only in its memory, so a restart carries on where the last pass stopped. The API never calls Kubernetes, and a slow install never holds a request open.
- **Each store is its own Helm release in its own namespace.** That namespace holds WordPress, MySQL, a setup Job, and the store's woo-mcp server. A ResourceQuota, a LimitRange, and default-deny NetworkPolicies surround it. Upgrading the platform never touches a store's data.
- **The dashboard** is a React app served by nginx. It talks only to `/api`, through a session cookie.
- **The chat agent** runs inside the gateway. For each question it mints a five-minute token for the signed-in person and calls the gateway's own `/mcp` with it. Chat calls therefore pass through the same permissions, rate limit, routing, and audit as any client.

## Tool aggregation

- The gateway keeps one MCP client session to each enabled upstream, each in its own goroutine.
- It exposes every connected upstream's tools on one shared MCP server as `<slug>__<tool>`. Slugs cannot contain `_`, so splitting at the first `__` always recovers the server, even when the upstream's own tool name contains `__`.
- When an upstream connects, drops, or changes its tools, the gateway adds or removes those tools on the shared server. The SDK then sends `tools/list_changed` to every open session.
- `tools/list` answers from memory, so it stays instant while an upstream is down.
- Per-person filtering sits in front of the list. Tool rules hide what a member may not use. In search mode the list holds only `mirage__search_tools` and `mirage__call_tool`, so a model with hundreds of tools behind the gateway fetches the schemas it needs.

## Routing

1. A request to `/mcp` carries an API key (`gw_`), an OAuth access token (`gwo_`), or the chat agent's internal token (`gwi_`). The gateway resolves it to a person, or answers 401 with a pointer to its OAuth metadata.
2. For `tools/call`, the gateway decodes the server slug from the tool name.
3. The tool rules decide whether the caller may use that tool. A refusal is an error result the model can read.
4. The per-person rate limit, counted in Postgres so it holds across replicas, decides whether the call may go now.
5. The gateway calls the upstream through its session, adding that server's own credential, with a 20-second timeout.
6. A deferred write records one audit row with the person, server, tool, duration, and outcome, however the call ends.

## Failure handling

- **Connecting.** Each upstream's handshake has a 10-second timeout. A failed upstream retries with exponential backoff from 1 to 30 seconds, with jitter, without touching the others.
- **Health.** Every 15 seconds the gateway lists each upstream's tools again. That keeps the tool cache current and detects a dead upstream.
- **Reporting.** An unreachable upstream carries a plain reason: connection refused, reset, or closed, timed out, host not found, an SSRF refusal, or the upstream's own error text. The Servers page and the Health page show it, and so do the call errors.
- **Calls.** Every failure becomes an `isError` result the model can act on, never a protocol error. The audit outcomes are `ok`, `tool_error`, `timeout`, `unreachable`, `canceled`, `rate_limited`, and `denied`.
- **Isolation of slow tools.** One slow tool holds only its own call. Other calls to the same or other upstreams proceed in parallel.
- **Replicas.** A session whose replica died answers 404, which tells the MCP client to open a new session. Pod disruption budgets, a pre-stop delay, and surge-only rolling updates keep a replica serving during upgrades.
- **Provisioning.** Every step is idempotent. A store has a 10-minute deadline and a hosted server has 3 minutes. Each one either becomes ready or fails with the reason that held it back. A hosted server whose upgrade fails keeps its previous version serving.

## Choices and their tradeoffs

| Choice | Instead of | Because |
| --- | --- | --- |
| One shared MCP server with per-person filtering | A server per session | One registry, the SDK's own session lifecycle, and instant `tools/list` |
| Upstream credentials held by the gateway | Passing each client's token through | Members use servers without ever holding their credentials, and revoking a person never touches upstreams |
| A level-triggered provisioner reading Postgres | Doing the work inside the API request | Restarts, retries, and deadlines all follow from one loop |
| Helm releases per store and per hosted server | Raw manifests from Go | `helm history` and `helm rollback` work on every store, and the charts are readable on their own |
| Session-owning replicas with signed session IDs | Sticky sessions at the ingress | Works behind any load balancer, and a forged ID cannot make the gateway proxy to an address of the caller's choosing |
| Rate limits counted in Postgres | Per-replica memory | The limit holds however many replicas run |
| Search mode as a request header | A second endpoint | OAuth tokens stay bound to one resource, `/mcp` |

## What changes for production

All of it is Helm values, in `charts/platform/values-prod.yaml`. [deploy/vps/README.md](../deploy/vps/README.md) has the full table.

- **DNS.** The dashboard host is `mirage.<domain>` and the MCP and OAuth host is `mcp.mirage.<domain>`. A wildcard `*.shop.mirage.<domain>` covers every store. Local runs use nip.io instead, which needs no setup.
- **Ingress.** k3s ships the same Traefik, so the same Ingress objects work. Every Ingress takes `ingress.tls.enabled` and `ingress.tls.clusterIssuer`, and cert-manager issues certificates from Let's Encrypt over HTTP-01. The gateway and stores switch to `https`, and session cookies become `Secure`.
- **Storage class.** Both clusters use their default class, `local-path`. Volume sizes and store quotas shrink to fit a 4 GB VM. A multi-node cluster sets `storageClass` to a network-backed class.
- **Secrets.** Values never hold passwords or keys. The chart generates the admin password, the admin API key, the session key, and the secretbox key on install, and it reuses them on every upgrade. The provisioner generates each store's passwords and seals them in Postgres. The operator creates only the chat model's key, with `kubectl create secret`, and backs up the `mirage-gateway` Secret.
- **Images.** `make push-images REGISTRY=... TAG=...` pushes every image, and the production values pin those tags.
