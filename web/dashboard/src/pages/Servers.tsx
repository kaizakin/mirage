import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type SubmitEvent } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/endpoints";
import type { HostedStatus, Server } from "../api/types";
import { Badge, type Tone } from "../components/Badge";
import { ConfirmButton } from "../components/ConfirmButton";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorNotice, Loading, MutationError } from "../components/States";
import { formatDate } from "../format";
import { statusTone } from "../tones";
import { ServerFields } from "./ServerFields";
import { draftFrom, emptyDraft, toNewServer, toPatch } from "./serverForm";

const serversKey = ["servers"] as const;

const hostedTone: Record<HostedStatus, Tone> = { deploying: "amber", running: "green", failed: "red" };

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
        <Icon name="chevron" size={14} />
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

function AuthSummary({ server }: { server: Server }) {
  if (server.auth_type === "none") return <span className="muted">No auth</span>;
  return (
    <span>
      {server.auth_type === "bearer" ? "Bearer" : <code>{server.auth_header}</code>}
      <span className="muted"> · {server.has_credential ? "credential stored" : "no credential"}</span>
    </span>
  );
}

function ServerCard({ server }: { server: Server }) {
  const client = useQueryClient();
  const [editing, setEditing] = useState(false);
  const refresh = () => client.invalidateQueries({ queryKey: serversKey });
  const toggle = useMutation({
    mutationFn: (enabled: boolean) => api.updateServer(server.slug, { enabled }),
    onSuccess: refresh,
  });
  const remove = useMutation({ mutationFn: () => api.deleteServer(server.slug), onSuccess: refresh });
  const error = toggle.error ?? remove.error;
  const managedNote = server.store === undefined ? undefined : `${server.slug}-managed`;

  return (
    <li className={`server server-${server.status}`}>
      <div className="server-main">
        <div className="server-id">
          <h2 className="server-slug">{server.slug}</h2>
          <Badge tone={statusTone[server.status]}>{server.status}</Badge>
          {server.store !== undefined && (
            <Link to="/stores" className="server-chip">
              Store: {server.store}
            </Link>
          )}
          {server.hosted && (
            <span className="server-chip" title={server.hosted.image}>
              Hosted
            </span>
          )}
        </div>
        <div className="server-url mono truncate" title={server.url}>
          {server.url}
        </div>
        <div className="row-actions">
          <label className="switch" title={server.store && `Managed by store ${server.store}`}>
            <input
              type="checkbox"
              role="switch"
              aria-label={`Enable ${server.slug}`}
              aria-describedby={managedNote}
              checked={server.enabled}
              disabled={toggle.isPending || server.store !== undefined}
              onChange={(e) => { toggle.mutate(e.target.checked); }}
            />
            <span>{server.enabled ? "Enabled" : "Disabled"}</span>
          </label>
          {server.store === undefined ? (
            <>
              <button
                type="button"
                className="button button-quiet"
                aria-expanded={editing}
                aria-label={`Edit ${server.slug}`}
                onClick={() => { setEditing((v) => !v); }}
              >
                Edit
              </button>
              <ConfirmButton
                label="Delete"
                subject={server.slug}
                confirmLabel={`Delete ${server.slug}`}
                busy={remove.isPending}
                onConfirm={() => { remove.mutate(); }}
              />
            </>
          ) : (
            <span id={managedNote} className="muted small">
              Managed by store {server.store}
            </span>
          )}
        </div>
      </div>
      {server.hosted?.reason && (
        <p className="reason">
          <Icon name="alert" size={14} />
          <span>Deployment failed: {server.hosted.reason}</span>
        </p>
      )}
      {server.reason && (
        <p className="reason">
          <Icon name="alert" size={14} />
          <span>{server.reason}</span>
        </p>
      )}
      <dl className="server-meta">
        <div>
          <dt>Auth</dt>
          <dd>
            <AuthSummary server={server} />
          </dd>
        </div>
        {server.hosted && (
          <div>
            <dt>Image</dt>
            <dd>
              <code>{server.hosted.image}</code>{" "}
              <Badge tone={hostedTone[server.hosted.status]}>{server.hosted.status}</Badge>
            </dd>
          </div>
        )}
        <div>
          <dt>Added</dt>
          <dd>{formatDate(server.created_at)}</dd>
        </div>
        <div className="server-tools">
          <dt>Tools</dt>
          <dd>
            <ToolList server={server} />
          </dd>
        </div>
      </dl>
      <MutationError error={error} />
      {editing && <EditServerForm server={server} onDone={() => { setEditing(false); }} />}
    </li>
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
            <Icon name="plus" />
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
        <EmptyState icon="servers" title="No servers yet">
          Add one to expose its tools through the gateway.
        </EmptyState>
      ) : (
        <ul className="server-list" aria-label="Servers">
          {servers.data.map((server) => (
            <ServerCard key={server.slug} server={server} />
          ))}
        </ul>
      )}
    </section>
  );
}
