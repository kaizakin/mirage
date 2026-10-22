import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type SubmitEvent } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router-dom";
import { errorMessage, isStatus } from "../api/client";
import { api } from "../api/endpoints";
import { BrandMark } from "../components/Icon";
import { Alert, Loading } from "../components/States";
import { safeNext, setSession, useMe } from "../session";
import { PasswordInput } from "../components/PasswordInput";

export function LoginPage() {
  const me = useMe();
  const client = useQueryClient();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const target = safeNext(params.get("next")) ?? "/";
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const login = useMutation({
    mutationFn: () => api.login(email, password),
    onSuccess: (user) => {
      setSession(client, user);
      void navigate(target, { replace: true });
    },
  });

  if (me.isPending) return <Loading />;
  if (me.data) return <Navigate to={target} replace />;

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    login.mutate();
  };

  return (
    <main className="auth-page">
      <form className="card auth-card" onSubmit={submit} aria-labelledby="login-title">
        <div className="auth-head">
          <BrandMark size={40} />
          <h1 id="login-title">Sign in to Mirage</h1>
          <p className="muted">The admin console for your MCP gateway.</p>
        </div>
        <div className="field">
          <label htmlFor="login-email">Email</label>
          <input
            id="login-email"
            type="email"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => { setEmail(e.target.value); }}
          />
        </div>
        <div className="field">
          <label htmlFor="login-password">Password</label>
          <PasswordInput
            id="login-password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => { setPassword(e.target.value); }}
          />
        </div>
        {login.isError && <Alert>{isStatus(login.error, 401) ? "Wrong email or password." : errorMessage(login.error)}</Alert>}
        <button type="submit" className="button button-primary" disabled={login.isPending}>
          {login.isPending ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </main>
  );
}
