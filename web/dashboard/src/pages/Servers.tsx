import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type SubmitEvent } from "react";
import { api } from "../api/endpoints";
import type { Server, ServerStatus } from "../api/types";
import { Badge, type Tone } from "../components/Badge";
import { ConfirmButton } from "../components/ConfirmButton";
import { ErrorNotice, Loading, MutationError } from "../components/States";
import { formatDate } from "../format";
import { ServerFields } from "./ServerFields";
import { draftFrom, emptyDraft, toNewServer, toPatch } from "./serverForm";

const serversKey = ["servers"] as const;

const statusTone: Record<ServerStatus, Tone> = {
  connected: "green",
  connecting: "amber",
  unreachable: "red",
  disabled: "grey",
};

function AddServerForm({ onDone }: { onDone: () => void }) {
  const client = useQueryClient();
  const [draft, setDraft] = useState(emptyDraft);
  const create = useMutation({
    mutationFn: () => api.createServer(toNewServer(draft)),
    onSuccess: async () => {
      setDraft(emptyDraft);
      await client.invalidateQueries({ queryKey: serversKey });
      onDone();
    },
  });

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    create.mutate();
  };

  return (
    <form className="card" onSubmit={submit} aria-labelledby="add-server-title">
      <h2 id="add-server-title">Add server</h2>
      <ServerFields idPrefix="add-server" mode="create" draft={draft} onChange={setDraft} />
      <MutationError error={create.error} />
      <div className="actions">
        <button type="submit" className="button button-primary" disabled={create.isPending}>
          {create.isPending ? "Adding…" : "Add server"}
        </button>
        <button type="button" className="button button-quiet" onClick={onDone}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function EditServerForm({ server, onDone }: { server: Server; onDone: () => void }) {
  const client = useQueryClient();
  const [draft, setDraft] = useState(() => draftFrom(server));
  const update = useMutation({
    mutationFn: () => api.updateServer(server.slug, toPatch(draft)),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: serversKey });
      onDone();
    },
  });

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    update.mutate();
  };

  return (
    <form className="inline-form" onSubmit={submit} aria-label={`Edit ${server.slug}`}>
      <ServerFields
        idPrefix={`edit-${server.slug}`}
        mode="edit"
        draft={draft}
        onChange={setDraft}
        hasCredential={server.has_credential}
      />
      <MutationError error={update.error} />
      <div className="actions">
        <button type="submit" className="button button-primary" disabled={update.isPending}>
          {update.isPending ? "Saving…" : "Save changes"}
        </button>
        <button type="button" className="button button-quiet" onClick={onDone}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function ToolList({ server }: { server: Server }) {
  if (server.tools.length === 0) return <span className="muted">No tools</span>;
  return (
    <details className="tools">
      <summary>
        {server.tools.length} {server.tools.length === 1 ? "tool" : "tools"}
      </summary>
      <dl>
        {server.tools.map((tool) => (
          <div key={tool.name} className="tool">
            <dt>
              <code>{tool.name}</code>
            </dt>
            <dd>{tool.description || <span className="muted">No description</span>}</dd>
          </div>
        ))}
      </dl>
    </details>
  );
}

function ServerRow({ server }: { server: Server }) {
  const client = useQueryClient();
  const [editing, setEditing] = useState(false);
  const refresh = () => client.invalidateQueries({ queryKey: serversKey });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.updateServer(server.slug, { enabled }),
    onSuccess: refresh,
  });
  const remove = useMutation({ mutationFn: () => api.deleteServer(server.slug), onSuccess: refresh });
  const error = toggle.error ?? remove.error;

  return (
    <>
      <tr>
        <td>
          <strong>{server.slug}</strong>
          <div className="muted small mono">{server.url}</div>
        </td>
        <td>
          <Badge tone={statusTone[server.status]}>{server.status}</Badge>
          {server.reason && <div className="reason">{server.reason}</div>}
        </td>
        <td>
          {server.auth_type === "none" ? (
            <span className="muted">None</span>
          ) : (
            <>
              {server.auth_type === "bearer" ? "Bearer" : <code>{server.auth_header}</code>}
              <div className="muted small">{server.has_credential ? "Credential stored" : "No credential"}</div>
            </>
          )}
        </td>
        <td>
          <ToolList server={server} />
        </td>
        <td className="nowrap">{formatDate(server.created_at)}</td>
        <td>
          <div className="row-actions">
            <label className="switch">
              <input
                type="checkbox"
                role="switch"
                checked={server.enabled}
                disabled={toggle.isPending}
                onChange={(e) => { toggle.mutate(e.target.checked); }}
              />
              <span>{server.enabled ? "Enabled" : "Disabled"}</span>
            </label>
            <button
              type="button"
              className="button button-quiet"
              aria-expanded={editing}
              onClick={() => { setEditing((v) => !v); }}
            >
              Edit
            </button>
            <ConfirmButton
              label="Delete"
              confirmLabel={`Delete ${server.slug}`}
              busy={remove.isPending}
              onConfirm={() => { remove.mutate(); }}
            />
          </div>
          <MutationError error={error} />
        </td>
      </tr>
      {editing && (
        <tr className="subrow">
          <td colSpan={6}>
            <EditServerForm server={server} onDone={() => { setEditing(false); }} />
          </td>
        </tr>
      )}
    </>
  );
}

export function ServersPage() {
  const servers = useQuery({ queryKey: serversKey, queryFn: api.servers, refetchInterval: 5000 });
  const [adding, setAdding] = useState(false);

  return (
    <section aria-labelledby="servers-title">
      <div className="page-head">
        <div>
          <h1 id="servers-title">Servers</h1>
          <p className="muted">Upstream MCP servers behind the gateway. Status refreshes every 5 seconds.</p>
        </div>
        {!adding && (
          <button type="button" className="button button-primary" onClick={() => { setAdding(true); }}>
            Add server
          </button>
        )}
      </div>
      {adding && <AddServerForm onDone={() => { setAdding(false); }} />}
      {servers.isPending ? (
        <Loading />
      ) : servers.isError && !servers.data ? (
        <ErrorNotice error={servers.error} />
      ) : servers.data.length === 0 ? (
        <p className="empty-state muted">No servers yet. Add one to expose its tools through the gateway.</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">Server</th>
                <th scope="col">Status</th>
                <th scope="col">Auth</th>
                <th scope="col">Tools</th>
                <th scope="col">Added</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {servers.data.map((server) => (
                <ServerRow key={server.slug} server={server} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
