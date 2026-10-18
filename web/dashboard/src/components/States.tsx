import { errorMessage, isStatus } from "../api/client";

export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <p className="muted loading" role="status">
      <span className="spinner" aria-hidden="true" />
      {label}
    </p>
  );
}

export function AdminsOnly() {
  return (
    <div className="empty-state">
      <h2>Admins only</h2>
      <p className="muted">Your account is a member. Ask an admin if you need access to this page.</p>
    </div>
  );
}

export function ErrorNotice({ error }: { error: unknown }) {
  if (isStatus(error, 403)) return <AdminsOnly />;
  return (
    <p className="alert alert-error" role="alert">
      {errorMessage(error)}
    </p>
  );
}

export function MutationError({ error }: { error: unknown }) {
  if (error === null || error === undefined) return null;
  return (
    <p className="alert alert-error" role="alert">
      {errorMessage(error)}
    </p>
  );
}
