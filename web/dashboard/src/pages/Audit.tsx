import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Fragment } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/endpoints";
import { oneOf, outcomes, type AuditEntry, type AuditFilter, type AuditOptions, type Outcome } from "../api/types";
import { Badge, type Tone } from "../components/Badge";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorNotice, Loading } from "../components/States";
import { formatDuration } from "../format";
import { useCurrentUser } from "../session";

const outcomeStyle: Record<Outcome, { label: string; tone: Tone }> = {
  ok: { label: "Success", tone: "green" },
  tool_error: { label: "Tool error", tone: "red" },
  timeout: { label: "Timed out", tone: "amber" },
  unreachable: { label: "Unreachable", tone: "red" },
  canceled: { label: "Canceled", tone: "grey" },
};

// The filter lives in the URL, so a filtered view survives a reload and can be shared.
function useFilter(): [AuditFilter, (next: AuditFilter) => void] {
  const [params, setParams] = useSearchParams();
  const filter: AuditFilter = {
    server: params.get("server") ?? "",
    tool: params.get("tool") ?? "",
    outcome: oneOf(outcomes, params.get("outcome") ?? "") ?? "",
    userId: Number(params.get("user")) || 0,
  };
  const setFilter = (next: AuditFilter) => {
    const out = new URLSearchParams();
    if (next.server) out.set("server", next.server);
    if (next.tool) out.set("tool", next.tool);
    if (next.outcome) out.set("outcome", next.outcome);
    if (next.userId) out.set("user", String(next.userId));
    setParams(out, { replace: true });
  };
  return [filter, setFilter];
}

const isFiltered = (f: AuditFilter) => Boolean(f.server || f.tool || f.outcome || f.userId);

// A tool option's value carries its server, so picking a tool under "Any server" selects both.
const toolValue = (server: string, tool: string) => `${server}/${tool}`;

function Filters({ filter, options, showUsers, onChange }: {
  filter: AuditFilter;
  options: AuditOptions;
  showUsers: boolean;
  onChange: (next: AuditFilter) => void;
}) {
  const servers = filter.server ? options.servers.filter((s) => s.slug === filter.server) : options.servers;
  return (
    <div className="filters" role="group" aria-label="Filter audit log">
      {showUsers && (
        <div className="field">
          <label htmlFor="audit-user">User</label>
          <select
            id="audit-user"
            value={filter.userId}
            onChange={(e) => { onChange({ ...filter, userId: Number(e.target.value) }); }}
          >
            <option value={0}>Everyone</option>
            {options.users.map((u) => (
              <option key={u.id} value={u.id}>
                {u.email}
              </option>
            ))}
          </select>
        </div>
      )}
      <div className="field">
        <label htmlFor="audit-server">Server</label>
        <select
          id="audit-server"
          value={filter.server}
          onChange={(e) => { onChange({ ...filter, server: e.target.value, tool: "" }); }}
        >
          <option value="">Any server</option>
          {options.servers.map((s) => (
            <option key={s.slug} value={s.slug}>
              {s.slug}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor="audit-tool">Tool</label>
        <select
          id="audit-tool"
          value={filter.tool ? toolValue(filter.server, filter.tool) : ""}
          onChange={(e) => {
            const [server = "", tool = ""] = e.target.value.split("/");
            onChange({ ...filter, server: tool ? server : filter.server, tool });
          }}
        >
          <option value="">Any tool</option>
          {servers.map((s) => (
            <optgroup key={s.slug} label={s.slug}>
              {s.tools.map((tool) => (
                <option key={tool} value={toolValue(s.slug, tool)}>
                  {tool}
                </option>
              ))}
            </optgroup>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor="audit-outcome">Result</label>
        <select
          id="audit-outcome"
          value={filter.outcome}
          onChange={(e) => { onChange({ ...filter, outcome: oneOf(outcomes, e.target.value) ?? "" }); }}
        >
          <option value="">Any result</option>
          {outcomes.map((outcome) => (
            <option key={outcome} value={outcome}>
              {outcomeStyle[outcome].label}
            </option>
          ))}
        </select>
      </div>
      {isFiltered(filter) && (
        <button
          type="button"
          className="button button-quiet clear-filters"
          onClick={() => { onChange({ server: "", tool: "", outcome: "", userId: 0 }); }}
        >
          Clear filters
        </button>
      )}
    </div>
  );
}

const dayFormat = new Intl.DateTimeFormat(undefined, { weekday: "long", day: "numeric", month: "long", year: "numeric" });
const timeFormat = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
const fullFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "full", timeStyle: "long" });

function dayLabel(date: Date): string {
  const today = new Date();
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (date.toDateString() === today.toDateString()) return "Today";
  if (date.toDateString() === yesterday.toDateString()) return "Yesterday";
  return dayFormat.format(date);
}

// groupByDay splits entries, already newest first, into runs that share a local calendar day.
function groupByDay(entries: AuditEntry[]): { day: string; entries: AuditEntry[] }[] {
  const groups: { day: string; entries: AuditEntry[] }[] = [];
  for (const entry of entries) {
    const day = dayLabel(new Date(entry.started_at));
    const last = groups.at(-1);
    if (last?.day === day) last.entries.push(entry);
    else groups.push({ day, entries: [entry] });
  }
  return groups;
}

export function AuditPage() {
  const me = useCurrentUser();
  const isAdmin = me.role === "admin";
  const [filter, setFilter] = useFilter();
  const options = useQuery({ queryKey: ["audit-options"], queryFn: api.auditOptions });
  const audit = useInfiniteQuery({
    queryKey: ["audit", filter],
    queryFn: ({ pageParam }) => api.audit(filter, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => last.next_before,
  });
  const entries = audit.data?.pages.flatMap((page) => page.entries) ?? [];
  const columns = isAdmin ? 6 : 5;

  return (
    <section aria-labelledby="audit-title">
      <div className="page-head">
        <div>
          <h1 id="audit-title">Audit log</h1>
          <p className="muted">
            {isAdmin ? "Every tool call through the gateway, newest first." : "Your tool calls through the gateway, newest first."}
          </p>
        </div>
        <button
          type="button"
          className="button"
          disabled={audit.isFetching}
          onClick={() => {
            void audit.refetch();
            void options.refetch();
          }}
        >
          <Icon name="refresh" />
          Refresh
        </button>
      </div>
      <Filters
        filter={filter}
        options={options.data ?? { servers: [], users: [] }}
        showUsers={isAdmin}
        onChange={setFilter}
      />
      {audit.isPending ? (
        <Loading />
      ) : audit.isError ? (
        <ErrorNotice error={audit.error} />
      ) : entries.length === 0 ? (
        <EmptyState icon="audit" title={isFiltered(filter) ? "No calls match these filters" : "No calls yet"}>
          {isFiltered(filter) ? "Try a different server, tool, or result." : "Tool calls through the gateway appear here as they happen."}
        </EmptyState>
      ) : (
        <>
          <div className="table-wrap">
            <table className="audit-table">
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  {isAdmin && <th scope="col">User</th>}
                  <th scope="col">Server</th>
                  <th scope="col">Tool</th>
                  <th scope="col">Result</th>
                  <th scope="col" className="numeric">
                    Duration
                  </th>
                </tr>
              </thead>
              {groupByDay(entries).map((group) => (
                <tbody key={group.day}>
                  <tr className="day-row">
                    <th scope="colgroup" colSpan={columns}>
                      {group.day}
                    </th>
                  </tr>
                  {group.entries.map((entry) => {
                    const started = new Date(entry.started_at);
                    const outcome = outcomeStyle[entry.outcome];
                    return (
                      <Fragment key={entry.id}>
                        <tr className={entry.error ? "has-detail" : undefined}>
                          <td className="audit-time nowrap">
                            <time dateTime={entry.started_at} title={fullFormat.format(started)}>
                              {timeFormat.format(started)}
                            </time>
                          </td>
                          {isAdmin && (
                            <td className="audit-user" title={entry.user_email}>
                              {entry.user_email || <span className="muted">deleted user</span>}
                            </td>
                          )}
                          <td className="audit-server">
                            <span className="server-chip">{entry.server}</span>
                          </td>
                          <td className="audit-tool mono">{entry.tool}</td>
                          <td className="audit-outcome">
                            <Badge tone={outcome.tone}>{outcome.label}</Badge>
                          </td>
                          <td className="numeric nowrap audit-duration">{formatDuration(entry.duration_ms)}</td>
                        </tr>
                        {entry.error && (
                          <tr className="detail-row">
                            <td colSpan={columns}>
                              <p className="reason" title={entry.error}>
                                <Icon name="alert" size={14} />
                                <span>{entry.error}</span>
                              </p>
                            </td>
                          </tr>
                        )}
                      </Fragment>
                    );
                  })}
                </tbody>
              ))}
            </table>
          </div>
          {audit.hasNextPage && (
            <div className="load-more">
              <button
                type="button"
                className="button"
                disabled={audit.isFetchingNextPage}
                onClick={() => void audit.fetchNextPage()}
              >
                {audit.isFetchingNextPage ? "Loading…" : "Load more"}
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}
