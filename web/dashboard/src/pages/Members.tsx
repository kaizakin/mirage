import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type SubmitEvent } from "react";
import { api } from "../api/endpoints";
import { oneOf, roles, type CreatedInvite, type Member, type Role } from "../api/types";
import { ConfirmButton } from "../components/ConfirmButton";
import { CopyField } from "../components/CopyField";
import { ErrorNotice, Loading, MutationError } from "../components/States";
import { formatDate } from "../format";
import { useCurrentUser } from "../session";

const membersKey = ["members"] as const;
const invitesKey = ["invites"] as const;

function RoleSelect({ id, value, onChange, disabled = false }: { id: string; value: Role; onChange: (role: Role) => void; disabled?: boolean }) {
  return (
    <select
      id={id}
      value={value}
      disabled={disabled}
      onChange={(e) => {
        const role = oneOf(roles, e.target.value);
        if (role) onChange(role);
      }}
    >
      {roles.map((role) => (
        <option key={role} value={role}>
          {role}
        </option>
      ))}
    </select>
  );
}

function MemberRow({ member, isSelf }: { member: Member; isSelf: boolean }) {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: membersKey });
  const setRole = useMutation({ mutationFn: (role: Role) => api.setMemberRole(member.id, role), onSuccess: refresh });
  const remove = useMutation({ mutationFn: () => api.removeMember(member.id), onSuccess: refresh });
  const selectId = `member-role-${member.id}`;

  return (
    <tr>
      <td>
        {member.email}
        {isSelf && <span className="muted small"> (you)</span>}
      </td>
      <td>
        <label htmlFor={selectId} className="visually-hidden">
          Role for {member.email}
        </label>
        <RoleSelect
          id={selectId}
          value={member.role}
          disabled={isSelf || setRole.isPending}
          onChange={(role) => { setRole.mutate(role); }}
        />
      </td>
      <td className="nowrap">{formatDate(member.created_at)}</td>
      <td>
        <ConfirmButton
          label="Remove"
          confirmLabel={`Remove ${member.email}`}
          disabled={isSelf}
          busy={remove.isPending}
          onConfirm={() => { remove.mutate(); }}
        />
        <MutationError error={setRole.error ?? remove.error} />
      </td>
    </tr>
  );
}

function InviteForm() {
  const client = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [created, setCreated] = useState<CreatedInvite | null>(null);
  const invite = useMutation({
    mutationFn: () => api.createInvite(email.trim(), role),
    onSuccess: async (result) => {
      setCreated(result);
      setEmail("");
      await client.invalidateQueries({ queryKey: invitesKey });
    },
  });

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    setCreated(null);
    invite.mutate();
  };

  return (
    <form className="card" onSubmit={submit} aria-labelledby="invite-title">
      <h2 id="invite-title">Invite member</h2>
      <div className="form-grid">
        <div className="field">
          <label htmlFor="invite-email">Email</label>
          <input id="invite-email" type="email" required value={email} onChange={(e) => { setEmail(e.target.value); }} />
        </div>
        <div className="field">
          <label htmlFor="invite-role">Role</label>
          <RoleSelect id="invite-role" value={role} onChange={setRole} />
        </div>
      </div>
      <MutationError error={invite.error} />
      <div className="actions">
        <button type="submit" className="button button-primary" disabled={invite.isPending}>
          {invite.isPending ? "Creating invite…" : "Create invite"}
        </button>
      </div>
      {created && (
        <div className="secret" role="region" aria-label="New invite link">
          <p className="alert alert-warning">
            This link is shown once. Copy it now and send it to {created.email}. It expires {formatDate(created.expires_at)}.
          </p>
          <CopyField label="Invite link" value={`${location.origin}/accept?token=${encodeURIComponent(created.token)}`} />
        </div>
      )}
    </form>
  );
}

function InviteTable() {
  const client = useQueryClient();
  const invites = useQuery({ queryKey: invitesKey, queryFn: api.invites });
  const revoke = useMutation({
    mutationFn: api.revokeInvite,
    onSuccess: () => client.invalidateQueries({ queryKey: invitesKey }),
  });

  if (invites.isPending) return <Loading />;
  if (invites.isError) return <ErrorNotice error={invites.error} />;
  if (invites.data.length === 0) return <p className="muted">No pending invites.</p>;
  return (
    <>
      <MutationError error={revoke.error} />
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th scope="col">Email</th>
              <th scope="col">Role</th>
              <th scope="col">Expires</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {invites.data.map((invite) => (
              <tr key={invite.id}>
                <td>{invite.email}</td>
                <td>{invite.role}</td>
                <td className="nowrap">{formatDate(invite.expires_at)}</td>
                <td>
                  <ConfirmButton
                    label="Revoke"
                    confirmLabel={`Revoke invite for ${invite.email}`}
                    busy={revoke.isPending && revoke.variables === invite.id}
                    onConfirm={() => { revoke.mutate(invite.id); }}
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

export function MembersPage() {
  const me = useCurrentUser();
  const members = useQuery({ queryKey: membersKey, queryFn: api.members });

  return (
    <section aria-labelledby="members-title">
      <div className="page-head">
        <div>
          <h1 id="members-title">Members</h1>
          <p className="muted">People who can sign in to the dashboard and hold gateway keys.</p>
        </div>
      </div>
      {members.isPending ? (
        <Loading />
      ) : members.isError ? (
        <ErrorNotice error={members.error} />
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">Email</th>
                <th scope="col">Role</th>
                <th scope="col">Joined</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {members.data.map((member) => (
                <MemberRow key={member.id} member={member} isSelf={member.id === me.id} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      <h2 className="section-title">Pending invites</h2>
      <InviteTable />
      <InviteForm />
    </section>
  );
}
