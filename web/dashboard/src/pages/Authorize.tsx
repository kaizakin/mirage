import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, type ReactNode } from "react";
import { Link, Navigate, useLocation } from "react-router-dom";
import { errorMessage } from "../api/client";
import { api } from "../api/endpoints";
import type { User } from "../api/types";
import { BrandMark } from "../components/Icon";
import { Alert, ErrorNotice, Loading } from "../components/States";
import { loginPath, useMe } from "../session";

function AuthCard({ titleId, children }: { titleId?: string; children: ReactNode }) {
  return (
    <main className="auth-page">
      <div className="card auth-card" role={titleId ? "region" : undefined} aria-labelledby={titleId}>
        {children}
      </div>
    </main>
  );
}

function sentence(message: string): string {
  return message.charAt(0).toUpperCase() + message.slice(1) + ".";
}

function InvalidLink({ error }: { error: unknown }) {
  return (
    <AuthCard titleId="authorize-title">
      <div className="auth-head">
        <BrandMark size={40} />
        <h1 id="authorize-title">Can't sign in</h1>
      </div>
      <Alert>{sentence(errorMessage(error))}</Alert>
      <Link to="/" className="auth-foot">
        Go to the dashboard
      </Link>
    </AuthCard>
  );
}

function Consent({ user, query }: { user: User; query: string }) {
  const check = useQuery({
    queryKey: ["oauth-authorize", query],
    queryFn: () => api.checkAuthorize(query),
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
  const decide = useMutation({
    mutationFn: (approve: boolean) => api.decideAuthorize(query, approve),
    onSuccess: ({ redirect }) => {
      window.location.assign(redirect);
    },
  });
  const clientError = check.data && "redirect" in check.data ? check.data.redirect : null;

  useEffect(() => {
    if (clientError !== null) window.location.assign(clientError);
  }, [clientError]);

  if (check.isPending) return <AuthCard><Loading /></AuthCard>;
  if (check.isError) return <InvalidLink error={check.error} />;
  if (!("client_name" in check.data)) return <AuthCard><Loading label="Returning to the app…" /></AuthCard>;

  const name = check.data.client_name;
  // The browser is still leaving after a successful answer, so the buttons stay off until it does.
  const busy = decide.isPending || decide.isSuccess;
  return (
    <AuthCard titleId="authorize-title">
      <div className="auth-head">
        <BrandMark size={40} />
        <h1 id="authorize-title">Allow {name} to use Mirage?</h1>
        <p className="muted">
          It will be able to call the gateway's tools as {user.email} until you revoke it from API keys › Connected apps.
        </p>
      </div>
      <p className="muted small auth-foot">
        Signed in as <strong>{user.email}</strong>
      </p>
      {decide.isError && <Alert>{sentence(errorMessage(decide.error))}</Alert>}
      <div className="consent-actions">
        <button type="button" className="button" disabled={busy} onClick={() => { decide.mutate(false); }}>
          {decide.isPending && !decide.variables ? "Denying…" : "Deny"}
        </button>
        <button type="button" className="button button-primary" disabled={busy} onClick={() => { decide.mutate(true); }}>
          {decide.isPending && decide.variables ? "Allowing…" : "Allow"}
        </button>
      </div>
    </AuthCard>
  );
}

// The gateway's OAuth authorization endpoint lands here; it sits outside the dashboard layout like Login.
export function AuthorizePage() {
  const me = useMe();
  const location = useLocation();
  if (me.isPending) return <AuthCard><Loading /></AuthCard>;
  if (me.isError) return <AuthCard><ErrorNotice error={me.error} /></AuthCard>;
  if (me.data === null) return <Navigate to={loginPath(location.pathname + location.search)} replace />;
  return <Consent user={me.data} query={location.search.slice(1)} />;
}
