import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/endpoints";
import type { ApiKey, CreatedKey } from "../api/types";
import { Badge } from "../components/Badge";
import { ConfirmButton } from "../components/ConfirmButton";
import { CopyField } from "../components/CopyField";
import { ErrorNotice, Loading, MutationError } from "../components/States";
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
      <td className="mono">{apiKey.id}</td>
      {showOwner && <td>{apiKey.user_email}</td>}
      <td>
        {apiKey.revoked_at === null ? <Badge tone="green">active</Badge> : <Badge tone="grey">revoked</Badge>}
      </td>
      <td className="nowrap">{formatDate(apiKey.created_at)}</td>
      <td className="nowrap">{apiKey.last_used_at ? formatDate(apiKey.last_used_at) : <span className="muted">Never</span>}</td>
      <td>
        {apiKey.revoked_at !== null ? (
          <span className="muted small">Revoked {formatDate(apiKey.revoked_at)}</span>
        ) : (
          <ConfirmButton label="Revoke" confirmLabel="Revoke key" busy={revoke.isPending} onConfirm={() => { revoke.mutate(); }} />
        )}
        <MutationError error={revoke.error} />
      </td>
    </tr>
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
        </div>
        <button type="button" className="button button-primary" disabled={generate.isPending} onClick={() => { generate.mutate(); }}>
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
        <p className="empty-state muted">No keys yet.</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">Key ID</th>
                {showAll && <th scope="col">User</th>}
                <th scope="col">Status</th>
                <th scope="col">Created</th>
                <th scope="col">Last used</th>
                <th scope="col">Actions</th>
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
    </section>
  );
}
