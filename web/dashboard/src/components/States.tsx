import type { ReactNode } from "react";
import { errorMessage, isStatus } from "../api/client";
import { Icon, type IconName } from "./Icon";

export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <p className="loading" role="status">
      <span className="spinner" aria-hidden="true" />
      {label}
    </p>
  );
}

export function EmptyState({ icon, title, children }: { icon: IconName; title: string; children?: ReactNode }) {
  return (
    <div className="empty-state">
      <span className="empty-icon">
        <Icon name={icon} size={20} />
      </span>
      <h2>{title}</h2>
      {children && <p className="muted">{children}</p>}
    </div>
  );
}

export function AdminsOnly() {
  return (
    <EmptyState icon="lock" title="Admins only">
      Your account is a member. Ask an admin if you need access to this page.
    </EmptyState>
  );
}

export function Alert({ children }: { children: ReactNode }) {
  return (
    <p className="alert alert-error" role="alert">
      <Icon name="alert" />
      <span>{children}</span>
    </p>
  );
}

export function ErrorNotice({ error }: { error: unknown }) {
  if (isStatus(error, 403)) return <AdminsOnly />;
  return <Alert>{errorMessage(error)}</Alert>;
}

export function MutationError({ error }: { error: unknown }) {
  if (error === null || error === undefined) return null;
  return <Alert>{errorMessage(error)}</Alert>;
}
