import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type SubmitEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { errorMessage, isStatus } from "../api/client";
import { api } from "../api/endpoints";
import { setSession } from "../session";

const minLength = 12;

function acceptError(error: unknown): string {
  if (isStatus(error, 404)) return "This invite link is invalid, already used, or expired. Ask an admin for a new one.";
  if (isStatus(error, 409)) return "An account with this email already exists. Sign in instead.";
  return errorMessage(error);
}

export function AcceptInvitePage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const client = useQueryClient();
  const navigate = useNavigate();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [mismatch, setMismatch] = useState(false);

  const accept = useMutation({
    mutationFn: () => api.acceptInvite(token, password),
    onSuccess: (user) => {
      setSession(client, user);
      void navigate("/", { replace: true });
    },
  });

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    if (password !== confirm) {
      setMismatch(true);
      return;
    }
    setMismatch(false);
    accept.mutate();
  };

  if (!token) {
    return (
      <main className="auth-page">
        <div className="card auth-card">
          <h1>Invite link incomplete</h1>
          <p className="muted">This link has no invite token. Open the full link an admin sent you.</p>
          <Link to="/login">Go to sign in</Link>
        </div>
      </main>
    );
  }

  return (
    <main className="auth-page">
      <form className="card auth-card" onSubmit={submit} aria-labelledby="accept-title">
        <h1 id="accept-title">Join Mirage</h1>
        <p className="muted">Choose a password for your new account.</p>
        <div className="field">
          <label htmlFor="accept-password">Password</label>
          <input
            id="accept-password"
            type="password"
            autoComplete="new-password"
            required
            minLength={minLength}
            aria-describedby="accept-password-hint"
            value={password}
            onChange={(e) => { setPassword(e.target.value); }}
          />
          <span id="accept-password-hint" className="hint">
            At least {minLength} characters.
          </span>
        </div>
        <div className="field">
          <label htmlFor="accept-confirm">Confirm password</label>
          <input
            id="accept-confirm"
            type="password"
            autoComplete="new-password"
            required
            aria-invalid={mismatch}
            value={confirm}
            onChange={(e) => { setConfirm(e.target.value); }}
          />
        </div>
        {mismatch && (
          <p className="alert alert-error" role="alert">
            The passwords do not match.
          </p>
        )}
        {accept.isError && (
          <p className="alert alert-error" role="alert">
            {acceptError(accept.error)}
          </p>
        )}
        <button type="submit" className="button button-primary" disabled={accept.isPending}>
          {accept.isPending ? "Creating account…" : "Create account"}
        </button>
        <p className="muted small">
          Already have an account? <Link to="/login">Sign in</Link>
        </p>
      </form>
    </main>
  );
}
