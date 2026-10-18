import { useInfiniteQuery } from "@tanstack/react-query";
import { useState, type SubmitEvent } from "react";
import { api } from "../api/endpoints";
import { oneOf, outcomes, type AuditFilter, type Outcome } from "../api/types";
import { Badge, type Tone } from "../components/Badge";
import { ErrorNotice, Loading } from "../components/States";
import { formatDuration, formatTimestamp } from "../format";

const outcomeTone: Record<Outcome, Tone> = {
  ok: "green",
  tool_error: "red",
  timeout: "amber",
  unreachable: "red",
  canceled: "grey",
};

const noFilter: AuditFilter = { server: "", tool: "", outcome: "" };

function FilterForm({ initial, onApply }: { initial: AuditFilter; onApply: (filter: AuditFilter) => void }) {
  const [draft, setDraft] = useState(initial);

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    onApply({ server: draft.server.trim(), tool: draft.tool.trim(), outcome: draft.outcome });
  };

  return (
    <form className="filters" onSubmit={submit} aria-label="Filter audit log">
      <div className="field">
        <label htmlFor="audit-server">Server</label>
        <input
          id="audit-server"
          placeholder="any"
          spellCheck={false}
          value={draft.server}
          onChange={(e) => { setDraft({ ...draft, server: e.target.value }); }}
        />
      </div>
      <div className="field">
        <label htmlFor="audit-tool">Tool</label>
        <input
          id="audit-tool"
          placeholder="any"
          spellCheck={false}
          value={draft.tool}
          onChange={(e) => { setDraft({ ...draft, tool: e.target.value }); }}
        />
      </div>
      <div className="field">
        <label htmlFor="audit-outcome">Outcome</label>
        <select
          id="audit-outcome"
          value={draft.outcome}
          onChange={(e) => { setDraft({ ...draft, outcome: oneOf(outcomes, e.target.value) ?? "" }); }}
        >
          <option value="">any</option>
          {outcomes.map((outcome) => (
            <option key={outcome} value={outcome}>
              {outcome}
            </option>
          ))}
        </select>
      </div>
      <div className="actions">
        <button type="submit" className="button button-primary">
          Apply
        </button>
        <button
          type="button"
          className="button button-quiet"
          onClick={() => {
            setDraft(noFilter);
            onApply(noFilter);
          }}
        >
          Clear
        </button>
      </div>
    </form>
  );
}

export function AuditPage() {
  const [filter, setFilter] = useState(noFilter);
  const audit = useInfiniteQuery({
    queryKey: ["audit", filter],
    queryFn: ({ pageParam }) => api.audit(filter, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => last.next_before,
  });
  const entries = audit.data?.pages.flatMap((page) => page.entries) ?? [];

  return (
    <section aria-labelledby="audit-title">
      <div className="page-head">
        <div>
          <h1 id="audit-title">Audit log</h1>
          <p className="muted">Every tool call through the gateway, newest first.</p>
        </div>
        <button type="button" className="button button-quiet" disabled={audit.isFetching} onClick={() => void audit.refetch()}>
          Refresh
        </button>
      </div>
      <FilterForm initial={filter} onApply={setFilter} />
      {audit.isPending ? (
        <Loading />
      ) : audit.isError ? (
        <ErrorNotice error={audit.error} />
      ) : entries.length === 0 ? (
        <p className="empty-state muted">No calls match.</p>
      ) : (
        <>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th scope="col">Time</th>
                  <th scope="col">User</th>
                  <th scope="col">Server</th>
                  <th scope="col">Tool</th>
                  <th scope="col">Outcome</th>
                  <th scope="col" className="numeric">
                    Duration
                  </th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => (
                  <tr key={entry.id}>
                    <td className="nowrap">{formatTimestamp(entry.started_at)}</td>
                    <td>{entry.user_email}</td>
                    <td className="mono">{entry.server}</td>
                    <td className="mono">{entry.tool}</td>
                    <td>
                      <Badge tone={outcomeTone[entry.outcome]}>{entry.outcome}</Badge>
                      {entry.error && <div className="reason">{entry.error}</div>}
                    </td>
                    <td className="numeric nowrap">{formatDuration(entry.duration_ms)}</td>
                  </tr>
                ))}
              </tbody>
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
