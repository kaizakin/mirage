import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type SubmitEvent } from "react";
import { api } from "../api/endpoints";
import type { Member, Server, ToolRule } from "../api/types";
import { Badge } from "../components/Badge";
import { ConfirmButton } from "../components/ConfirmButton";
import { ErrorNotice, Loading, MutationError } from "../components/States";
import { formatDate } from "../format";

const rulesKey = ["tool-rules"] as const;

function describe(rule: ToolRule): string {
  const who = rule.user_id === 0 ? "every member" : (rule.user_email ?? `user ${String(rule.user_id)}`);
  const what = rule.tool === "" ? "every tool" : rule.tool;
  const where = rule.server === "" ? "every server" : rule.server;
  return `${what} on ${where} for ${who}`;
}

function RuleForm({ members, servers }: { members: Member[]; servers: Server[] }) {
  const client = useQueryClient();
  const [userId, setUserId] = useState(0);
  const [server, setServer] = useState("");
  const [tool, setTool] = useState("");
  const [allow, setAllow] = useState(false);
  const create = useMutation({
    mutationFn: () => api.createToolRule({ user_id: userId, server, tool: tool.trim(), allow }),
    onSuccess: async () => {
      setTool("");
      await client.invalidateQueries({ queryKey: rulesKey });
    },
  });

  // With every server chosen, offer each tool name any server has, for rules like
  // "no member may create_product on any store".
  const toolNames = [
    ...new Set((server === "" ? servers : servers.filter((s) => s.slug === server)).flatMap((s) => s.tools.map((t) => t.name))),
  ].sort();

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    create.mutate();
  };

  return (
    <form className="card invite-card" onSubmit={submit} aria-labelledby="rule-title">
      <h2 id="rule-title">Add a rule</h2>
      <div className="invite-row">
        <div className="field">
          <label htmlFor="rule-effect">Effect</label>
          <select id="rule-effect" value={allow ? "allow" : "deny"} onChange={(e) => { setAllow(e.target.value === "allow"); }}>
            <option value="deny">Deny</option>
            <option value="allow">Allow</option>
          </select>
        </div>
        <div className="field">
          <label htmlFor="rule-user">Applies to</label>
          <select id="rule-user" value={userId} onChange={(e) => { setUserId(Number(e.target.value)); }}>
            <option value={0}>Every member</option>
            {members
              .filter((m) => m.role === "member")
              .map((m) => (
                <option key={m.id} value={m.id}>
                  {m.email}
                </option>
              ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor="rule-server">Server</label>
          <select id="rule-server" value={server} onChange={(e) => { setServer(e.target.value); setTool(""); }}>
            <option value="">Every server</option>
            {servers.map((s) => (
              <option key={s.slug} value={s.slug}>
                {s.slug}
              </option>
            ))}
          </select>
        </div>
        <div className="field invite-email">
          <label htmlFor="rule-tool">Tool</label>
          <input
            id="rule-tool"
            list="rule-tool-names"
            placeholder="Every tool"
            value={tool}
            onChange={(e) => { setTool(e.target.value); }}
          />
          <datalist id="rule-tool-names">
            {toolNames.map((name) => (
              <option key={name} value={name} />
            ))}
          </datalist>
        </div>
        <button type="submit" className="button button-primary" disabled={create.isPending}>
          {create.isPending ? "Adding…" : "Add rule"}
        </button>
      </div>
      <MutationError error={create.error} />
    </form>
  );
}

function RuleTable({ rules }: { rules: ToolRule[] }) {
  const client = useQueryClient();
  const remove = useMutation({
    mutationFn: api.deleteToolRule,
    onSuccess: () => client.invalidateQueries({ queryKey: rulesKey }),
  });

  if (rules.length === 0) return <p className="muted quiet-empty">No rules. Members may use every tool.</p>;
  return (
    <>
      <MutationError error={remove.error} />
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">Effect</th>
              <th scope="col">Applies to</th>
              <th scope="col">Server</th>
              <th scope="col">Tool</th>
              <th scope="col">Added</th>
              <th scope="col" className="actions-cell">
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            {rules.map((rule) => (
              <tr key={rule.id}>
                <td>
                  <Badge tone={rule.allow ? "green" : "red"}>{rule.allow ? "Allow" : "Deny"}</Badge>
                </td>
                <td>{rule.user_id === 0 ? <span className="muted">Every member</span> : rule.user_email}</td>
                <td>{rule.server === "" ? <span className="muted">Every server</span> : <code>{rule.server}</code>}</td>
                <td>{rule.tool === "" ? <span className="muted">Every tool</span> : <code>{rule.tool}</code>}</td>
                <td className="date">{formatDate(rule.created_at)}</td>
                <td className="actions-cell">
                  <ConfirmButton
                    label="Delete"
                    subject={`the rule for ${describe(rule)}`}
                    confirmLabel={`Delete the rule for ${describe(rule)}`}
                    busy={remove.isPending && remove.variables === rule.id}
                    onConfirm={() => { remove.mutate(rule.id); }}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

export function PermissionsPage() {
  const rules = useQuery({ queryKey: rulesKey, queryFn: api.toolRules });
  const members = useQuery({ queryKey: ["members"], queryFn: api.members });
  const servers = useQuery({ queryKey: ["servers"], queryFn: api.servers });

  return (
    <section aria-labelledby="permissions-title">
      <div className="page-head">
        <div>
          <h1 id="permissions-title">Permissions</h1>
          <p className="muted">
            Which tools members may use. Admins may use every tool. A denied tool is hidden from the member&apos;s tool list
            and refused if called. When several rules match a call, the most specific wins: a member&apos;s own rule beats
            one for every member, then a named server beats every server, then a named tool beats every tool.
          </p>
        </div>
      </div>
      {members.isError || servers.isError ? (
        <ErrorNotice error={members.error ?? servers.error} />
      ) : members.isPending || servers.isPending ? (
        <Loading />
      ) : (
        <RuleForm members={members.data} servers={servers.data} />
      )}
      <h2 className="section-title">Rules</h2>
      {rules.isPending ? <Loading /> : rules.isError ? <ErrorNotice error={rules.error} /> : <RuleTable rules={rules.data} />}
    </section>
  );
}
