# Tradeoffs

Decisions where a reasonable alternative existed, with the reasoning, so they can be revisited when the reasoning stops holding.

## Downstream MCP endpoint: one shared server, not one server per user

**Decision.** The gateway runs a single `mcp.Server` for every client. Its tool registry mirrors the upstream manager: when an upstream connects, disconnects, or changes its tools, the gateway adds or removes the prefixed tools (`<slug>__<tool>`) on that one server. Per-user permissions, when they arrive, are receiving middleware that filters `tools/list` results and rejects disallowed `tools/call` requests.

**Alternative.** The streamable HTTP handler's `getServer(*http.Request)` callback can build a fresh `mcp.Server` for each session, registering only the tools that session's user may use.

| | Shared server + middleware | Server per session |
|---|---|---|
| Tool registry | One, updated once per upstream change | One per live session, each updated on every change |
| `list_changed` | The SDK broadcasts it when tools are added or removed | Same, per server |
| Session lifecycle | The SDK's | The gateway must track servers and drop them when their sessions end; the SDK has no hook for that |
| Authorization | A filter on list plus a check on call; both must be applied | Implicit: an unregistered tool cannot be listed or called |
| Permission change | Takes effect on the next request | Requires rebuilding the user's live servers |

**Why.** The shared server keeps one source of truth with the SDK's own lifecycle, and `tools/list` answers from memory, which is why the list stays instant while an upstream is down. Its weakness is that authorization is two checks that must agree. That is contained by putting both in one place, `internal/gateway/policy`, whose contract is already "filter on list, reject on call". The per-session design makes authorization implicit, but the price is a registry per session and lifecycle tracking the SDK does not support, paid on every upstream health change.

**Revisit if** permissions become per-session rather than per-user or role, or the number of concurrent sessions makes broadcast `list_changed` notifications costly.

## Upstream sessions: no standalone server-sent event stream

**Decision.** The gateway connects to upstreams with `DisableStandaloneSSE` and finds tool changes by re-listing every 15 seconds.

**Why.** DeepWiki is stateless and holds the standalone GET open without answering, which stalled `Connect` for over 15 seconds. The gateway needs no server-initiated messages from upstreams, and tool changes reaching clients up to 15 seconds late is acceptable.

**Revisit if** an upstream needs to push messages the gateway must relay, such as progress for long-running tools outside a call's own response stream.

## Search mode: a request header, not a separate endpoint

**Decision.** A client opts into the two-tool search mode with the `X-Mirage-Tools: search` header on the same `/mcp` endpoint. The meta-tools are registered on the shared server for every session and listed only in search mode.

**Alternative.** A second endpoint, such as `/mcp/search`, backed by its own `mcp.Server`.

**Why.** OAuth access tokens are bound to the `/mcp` resource, so a second path would need its own protected-resource metadata and audience. A header keeps one endpoint, one token, and one registry. `mirage__call_tool` hands the call to the same `callTool` path as a direct call, so policy, rate limit and audit cannot differ between the modes.

**Revisit if** a client needs search mode but cannot send custom headers.

## Hosted servers: a failed deployment keeps its resources

**Decision.** When a hosted server's rollout fails or times out, the provisioner marks it failed and leaves the release as it is. Stores, by contrast, free their namespace on failure.

**Why.** A hosted server's failure is usually a bad upgrade. The Deployment's rolling update keeps the previous ReplicaSet serving, so leaving it alone keeps the tools working until an admin sets a good image. A brand-new server that never ran costs at most its small quota while it crash-loops.

**Revisit if** failed servers pile up and their quota matters.

## Provisioner rights: per-namespace bindings plus an admission policy

**Decision.** The provisioner's ClusterRole covers only namespaces, creating RoleBindings, and `bind` on one tenant ClusterRole. It binds the tenant role to itself in each namespace it creates. A ValidatingAdmissionPolicy refuses any write from its service account outside namespaces labelled `app.kubernetes.io/managed-by: mirage-provisioner`, and any create or delete of a namespace without that label.

**Alternative.** One ClusterRole with every right the store chart needs, which is what it had before.

**Why.** RBAC cannot scope rights to namespaces that do not exist yet or select namespaces by label, so a plain ClusterRole let the provisioner read the platform's own Secrets. The binding gives it rights only where it works. Holding "create RoleBindings anywhere" alone would let it bind the tenant role in the platform namespace, and the admission policy closes that.

**Revisit if** the cluster is older than Kubernetes 1.30, where ValidatingAdmissionPolicy is not GA. Without it, use a webhook or accept the ClusterRole.
