# Demo script

A walkthrough of the gateway in front of three upstreams (woo-mcp in the cluster, currency-mcp in the cluster, and DeepWiki on the internet), with what was observed on a local k3d cluster on 2026-10-24.

## Setup

```bash
make helm-upgrade
```

```bash
kubectl -n mirage get secret mirage-gateway -o jsonpath='{.data.admin-api-key}' | base64 -d
```

```bash
claude mcp add --transport http gw http://gateway.127.0.0.1.nip.io/mcp --header "Authorization: Bearer <admin API key>"
```

`claude mcp list` shows `gw ... ✔ Connected`.

## 1. One endpoint, every upstream's tools

Ask Claude Code which tools it has from `gw`. Observed: ten tools, each prefixed with its server's slug.

```text
currency__convert          deepwiki__ask_wiki_question     woo__create_product
currency__slow_echo        deepwiki__read_wiki_contents    woo__get_order
                           deepwiki__read_wiki_structure   woo__list_orders
                                                           woo__list_products
                                                           woo__update_order_status
```

## 2. A real question

Ask "what orders came in today?". Observed: Claude Code called `woo__list_orders` with `after: "today"` and answered with order #17, from Ada Lovelace for 2 × Seed Mug at $598.00, cash on delivery, in processing. "Today" is the store's local day, resolved by woo-mcp from the WordPress timezone.

## 3. Failure drills

`make e2e` runs these against one MCP session held open through the ingress, and fails on the first behavior that does not hold.

### An upstream pod dies mid-session

The drill deletes the woo-mcp pod while the session is open.

- `currency__convert` keeps answering normally.
- `woo__list_products` fails as a tool error rather than a protocol error: `upstream woo is unreachable: session not found`. The old MCP session died with the pod. Calls in the next few seconds say `connection closed` while the new pod starts.
- Woo recovers without intervention. The upstream manager reconnects with backoff, and the next call succeeds about 7 to 9 seconds after the delete.

### A slow tool hits the call timeout

The drill calls `currency__slow_echo` with `seconds: 60`.

- After exactly 20 seconds it returns the tool error `upstream currency did not answer slow_echo within 20s`.
- Three `currency__convert` calls sent while it was pending each answered within a second. A slow call holds up nothing else, even on the same upstream and session.

### A server is disabled in the database

```sql
UPDATE servers SET enabled = false WHERE slug = 'deepwiki';
```

- Within about 5 seconds, which is the gateway's poll interval, DeepWiki's three tools leave `tools/list` (10 tools become 7).
- The gateway pushes `notifications/tools/list_changed` on the session's event stream. A client that honors it, such as Claude Code, refreshes its tool list. Run `/mcp` in an interactive session to watch it.
- Setting `enabled = true` brings the tools back within a few seconds, with another `list_changed`.

### Everything is audited

Every call above has an `audit_log` row with its user, server, tool, duration, and outcome: `ok`, `tool_error`, `unreachable`, `timeout`, or `canceled`. Failures carry the same message the model saw:

```text
woo__list_products    unreachable  upstream woo is unreachable: session not found
woo__list_products    unreachable  upstream woo is unreachable: connection closed
woo__list_products    ok
currency__slow_echo   timeout      upstream currency did not answer slow_echo within 20s
```

## 4. Revoking a key takes effect mid-session

Open a session, make a call, then revoke the key in the database:

```sql
UPDATE api_keys SET revoked_at = now() WHERE key_prefix = '<id from gw_<id>_...>';
```

Observed: the next request on the same session gets HTTP 401. The key is checked on every request, not only at `initialize`. The response body says only `invalid API key`; the reason (revoked) is in the gateway's log.

## 5. An upstream goes away and comes back

```bash
kubectl -n mirage scale deploy mirage-currency-mcp --replicas=0
```

Observed in the gateway log: `upstream unreachable` with reason `connection refused`, retried at 1s, 2s, 4s, 8s, and 16s, while `tools/list` keeps answering in about 1ms without currency's tools. Scaling back to 1 brings `upstream connected` on the next retry, with no restart and no operator action.
