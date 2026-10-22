import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/endpoints";
import type { ApiKey, ConnectedApp, CreatedKey } from "../api/types";
import { Badge } from "../components/Badge";
import { ConfirmButton } from "../components/ConfirmButton";
import { CopyField } from "../components/CopyField";
import { Icon } from "../components/Icon";
import { EmptyState, ErrorNotice, Loading, MutationError } from "../components/States";
import { formatDate } from "../format";
import { useCurrentUser } from "../session";

const gatewayUrl = "http://gateway.127.0.0.1.nip.io/mcp";

function connectCommand(key: string): string {
  return `claude mcp add --transport http gw ${gatewayUrl} --header "Authorization: Bearer ${key}"`;
}

function NewKey({ created }: { created: CreatedKey }) {
  return (
    <div className="card secret" role="region" aria-label="New API key">
      <h2>Your new key</h2>
      <p className="alert alert-warning">Store it now. It won't be shown again.</p>
      <CopyField label="API key" value={created.key} />
      <CopyField label="Connect Claude Code" value={connectCommand(created.key)} multiline />
    </div>
  );
}

function KeyRow({ apiKey, showOwner }: { apiKey: ApiKey; showOwner: boolean }) {
  const client = useQueryClient();
  const revoke = useMutation({
    mutationFn: () => api.revokeKey(apiKey.id),
    onSuccess: () => client.invalidateQueries({ queryKey: ["keys"] }),
  });

  return (
    <tr>
      <td>
        <span className="mono key-id">{apiKey.id}</span>
        {showOwner && <span className="muted small block">{apiKey.user_email}</span>}
      </td>
      <td>
        {apiKey.revoked_at === null ? <Badge tone="green">active</Badge> : <Badge tone="grey">revoked</Badge>}
      </td>
      <td className="date">{formatDate(apiKey.created_at)}</td>
      <td className="date">{apiKey.last_used_at ? formatDate(apiKey.last_used_at) : <span className="muted">Never</span>}</td>
      <td className="actions-cell">
        {apiKey.revoked_at !== null ? (
          <span className="muted small">Revoked {formatDate(apiKey.revoked_at)}</span>
        ) : (
          <ConfirmButton label="Revoke" subject={`key ${apiKey.id}`} confirmLabel="Revoke key" busy={revoke.isPending} onConfirm={() => { revoke.mutate(); }} />
        )}
        <MutationError error={revoke.error} />
      </td>
    </tr>
  );
}

function AppRow({ app }: { app: ConnectedApp }) {
  const client = useQueryClient();
  const revoke = useMutation({
    mutationFn: () => api.revokeConnectedApp(app.id),
    onSuccess: () => client.invalidateQueries({ queryKey: ["connected-apps"] }),
  });

  return (
    <tr>
      <td>{app.client_name}</td>
      <td className="date">{formatDate(app.created_at)}</td>
      <td className="date">{app.last_used_at ? formatDate(app.last_used_at) : <span className="muted">Never</span>}</td>
      <td className="actions-cell">
        <ConfirmButton label="Revoke" subject={app.client_name} confirmLabel="Sign out app" busy={revoke.isPending} onConfirm={() => { revoke.mutate(); }} />
        <MutationError error={revoke.error} />
      </td>
    </tr>
  );
}

function ConnectedApps() {
  const apps = useQuery({ queryKey: ["connected-apps"], queryFn: api.connectedApps });

  return (
    <section aria-labelledby="apps-title">
      <h2 id="apps-title" className="section-title">
        Connected apps
      </h2>
      <p className="muted">Apps you signed in to with your browser, such as Claude Code. Revoking one signs it out immediately.</p>
      {apps.isPending ? (
        <Loading />
      ) : apps.isError ? (
        <ErrorNotice error={apps.error} />
      ) : apps.data.length === 0 ? (
        <EmptyState icon="lock" title="No connected apps">
          Apps appear here after you allow them on the browser sign-in screen.
        </EmptyState>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">App</th>
                <th scope="col">Connected</th>
                <th scope="col">Last used</th>
                <th scope="col" className="actions-cell">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody>
              {apps.data.map((app) => (
                <AppRow key={app.id} app={app} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

export function KeysPage() {
  const user = useCurrentUser();
  const client = useQueryClient();
  const [all, setAll] = useState(false);
  const showAll = user.role === "admin" && all;
  const keys = useQuery({ queryKey: ["keys", showAll], queryFn: () => api.keys(showAll) });
  const [created, setCreated] = useState<CreatedKey | null>(null);
  const generate = useMutation({
    mutationFn: api.createKey,
    onSuccess: async (key) => {
      setCreated(key);
      await client.invalidateQueries({ queryKey: ["keys"] });
    },
  });

  return (
    <section aria-labelledby="keys-title">
      <div className="page-head">
        <div>
          <h1 id="keys-title">API keys</h1>
          <p className="muted">Keys let MCP clients call tools through the gateway as you.</p>
          <p className="muted small">
            Claude Code can also sign in with your browser: <code>claude mcp add --transport http gw {gatewayUrl}</code>,
            then run <code>/mcp</code> in Claude Code and choose Authenticate.
          </p>
        </div>
        <button type="button" className="button button-primary" disabled={generate.isPending} onClick={() => { generate.mutate(); }}>
          <Icon name="plus" />
          {generate.isPending ? "Generating…" : "Generate key"}
        </button>
      </div>
      <MutationError error={generate.error} />
      {created && <NewKey created={created} />}
      {user.role === "admin" && (
        <label className="switch toolbar">
          <input type="checkbox" role="switch" checked={all} onChange={(e) => { setAll(e.target.checked); }} />
          <span>Show every user's keys</span>
        </label>
      )}
      {keys.isPending ? (
        <Loading />
      ) : keys.isError ? (
        <ErrorNotice error={keys.error} />
      ) : keys.data.length === 0 ? (
        <EmptyState icon="key" title="No keys yet">
          Generate a key to connect an MCP client such as Claude Code.
        </EmptyState>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">{showAll ? "Key ID · owner" : "Key ID"}</th>
                <th scope="col">Status</th>
                <th scope="col">Created</th>
                <th scope="col">Last used</th>
                <th scope="col" className="actions-cell">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody>
              {keys.data.map((key) => (
                <KeyRow key={key.id} apiKey={key} showOwner={showAll} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ConnectedApps />
    </section>
  );
}
