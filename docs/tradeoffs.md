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
