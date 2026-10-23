import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api/endpoints";
import type { HealthStatus, Outcome, ServerHealth } from "../api/types";
import { Badge, type Tone } from "../components/Badge";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorNotice, Loading } from "../components/States";
import { formatDuration, formatRelative, formatTimestamp } from "../format";
import { outcomeStyle, statusTone } from "../tones";

const healthTone: Record<HealthStatus, Tone> = { ...statusTone, removed: "grey" };

const count = new Intl.NumberFormat();
const percent = new Intl.NumberFormat(undefined, {
  style: "percent",
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
});

type RateLevel = "ok" | "warn" | "bad";

function rateLevel(rate: number): RateLevel {
  if (rate >= 0.2) return "bad";
  if (rate >= 0.05) return "warn";
  return "ok";
}

function ErrorRate({ calls, failed }: { calls: number; failed: number }) {
  const rate = calls === 0 ? 0 : failed / calls;
  return (
    <span className={`rate rate-${rateLevel(rate)}`}>
      <span>{percent.format(rate)}</span>
      <span className="rate-bar" aria-hidden="true">
        <span style={{ width: `${Math.min(rate, 1) * 100}%` }} />
      </span>
    </span>
  );
}

function Summary({ servers }: { servers: ServerHealth[] }) {
  const configured = servers.filter((s) => s.status !== "removed");
  const connected = configured.filter((s) => s.status === "connected").length;
  const unreachable = configured.filter((s) => s.status === "unreachable").length;
  const calls = servers.reduce((sum, s) => sum + s.calls, 0);
  const failed = servers.reduce((sum, s) => sum + s.failed, 0);

  return (
    <div role="status" aria-label="Health summary">
      <dl className="health-summary">
        <div>
          <dt>Connected</dt>
          <dd>
            {connected}
            <span className="health-summary-of"> of {configured.length}</span>
          </dd>
        </div>
        <div>
          <dt>Calls, last hour</dt>
          <dd>{count.format(calls)}</dd>
        </div>
        <div>
          <dt>Error rate</dt>
          <dd>
            <ErrorRate calls={calls} failed={failed} />
          </dd>
        </div>
        <div className={unreachable > 0 ? "health-summary-alarm" : undefined}>
          <dt>Unreachable</dt>
          <dd>{unreachable}</dd>
        </div>
      </dl>
    </div>
  );
}

function When({ iso, now }: { iso: string; now: number }) {
  return (
    <time dateTime={iso} title={formatTimestamp(iso)}>
      {formatRelative(iso, now)}
    </time>
  );
}

function LatestFailure({ server, outcome, at, message, now }: {
  server: string;
  outcome: Outcome;
  at: string;
  message: string | undefined;
  now: number;
}) {
  const style = outcomeStyle[outcome];
  const params = new URLSearchParams({ server, outcome });
  return (
    <div className="health-failure">
      <div className="health-failure-head">
        <span className="health-label">Latest failure</span>
        <Badge tone={style.tone}>{style.label}</Badge>
        <span className="muted small">
          <When iso={at} now={now} />
        </span>
        <Link className="health-audit-link" to={`/audit?${params.toString()}`}>
          See in audit log<span className="visually-hidden"> for {server}</span>
          <Icon name="chevron" size={14} />
        </Link>
      </div>
      {message && (
        <p className="health-error mono" title={message}>
          {message}
        </p>
      )}
    </div>
  );
}

function HealthCard({ health, now }: { health: ServerHealth; now: number }) {
  return (
    <li className={`server server-${health.status}`}>
      <div className="server-id">
        <h2 className="server-slug mono">{health.server}</h2>
        <Badge tone={healthTone[health.status]}>{health.status}</Badge>
        {health.since ? (
          <span className="muted small">
            since <When iso={health.since} now={now} />
          </span>
        ) : (
          health.status === "removed" && <span className="muted small">Deleted server, calls still in the last hour</span>
        )}
      </div>
      {health.status === "unreachable" && health.reason && (
        <p className="reason">
          <Icon name="alert" size={14} />
          <span>Unreachable: {health.reason}</span>
        </p>
      )}
      {health.calls === 0 ? (
        <p className="muted small health-idle">No calls in the last hour</p>
      ) : (
        <dl className="server-meta health-stats">
          <div>
            <dt>Calls</dt>
            <dd>
              {count.format(health.calls)}
              {health.failed > 0 && <span className="muted"> · {count.format(health.failed)} failed</span>}
            </dd>
          </div>
          <div>
            <dt>Error rate</dt>
            <dd>
              <ErrorRate calls={health.calls} failed={health.failed} />
            </dd>
          </div>
          <div>
            <dt>p50</dt>
            <dd>{formatDuration(health.p50_ms)}</dd>
          </div>
          <div>
            <dt>p95</dt>
            <dd>{formatDuration(health.p95_ms)}</dd>
          </div>
        </dl>
      )}
      {health.last_outcome && health.last_error_at && (
        <LatestFailure
          server={health.server}
          outcome={health.last_outcome}
          at={health.last_error_at}
          message={health.last_error}
          now={now}
        />
      )}
    </li>
  );
}

export function HealthPage() {
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, refetchInterval: 10_000 });

  return (
    <section aria-labelledby="health-title">
      <div className="page-head">
        <div>
          <h1 id="health-title">Health</h1>
          <p className="muted">Live connection status and the last hour of tool calls for every MCP server.</p>
        </div>
      </div>
      {health.isPending ? (
        <Loading />
      ) : health.isError && !health.data ? (
        <ErrorNotice error={health.error} />
      ) : health.data.servers.length === 0 ? (
        <EmptyState icon="pulse" title="No servers yet">
          Add a server and its health shows up here.
        </EmptyState>
      ) : (
        <>
          <Summary servers={health.data.servers} />
          <ul className="server-list health-list" aria-label="Server health">
            {health.data.servers.map((server) => (
              <HealthCard key={server.server} health={server} now={health.dataUpdatedAt} />
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
