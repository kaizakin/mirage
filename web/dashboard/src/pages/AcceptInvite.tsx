import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef, useState, type SubmitEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { errorMessage, isStatus } from "../api/client";
import { api } from "../api/endpoints";
import { BrandMark } from "../components/Icon";
import { Alert } from "../components/States";
import { setSession } from "../session";

const minLength = 12;

function acceptError(error: unknown): string {
  if (isStatus(error, 404)) return "This invite link is invalid, already used, or expired. Ask an admin for a new one.";
  if (isStatus(error, 409)) return "An account with this email already exists. Sign in instead.";
  return errorMessage(error);
}

// passwordProblem says what is wrong with a new password, or returns null when it is fine.
function passwordProblem(password: string, confirm: string): string | null {
  if (password.length < minLength) {
    const short = minLength - password.length;
    return `Your password needs at least ${minLength} characters. Add ${short} more.`;
  }
  if (password !== confirm) return "The two passwords do not match.";
  return null;
}

export function AcceptInvitePage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const client = useQueryClient();
  const navigate = useNavigate();
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const passwordField = useRef<HTMLInputElement>(null);

  const accept = useMutation({
    mutationFn: () => api.acceptInvite(token, password),
    onSuccess: (user) => {
      setSession(client, user);
      void navigate("/", { replace: true });
    },
  });

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    const found = passwordProblem(password, confirm);
    setProblem(found);
    if (found) {
      passwordField.current?.focus();
      return;
    }
    accept.mutate();
  };

  if (!token) {
    return (
      <main className="auth-page">
        <div className="card auth-card">
          <div className="auth-head">
            <BrandMark size={40} />
            <h1>Invite link incomplete</h1>
            <p className="muted">This link has no invite token. Open the full link an admin sent you.</p>
          </div>
          <Link to="/login">Go to sign in</Link>
        </div>
      </main>
    );
  }

  return (
    <main className="auth-page">
      <form className="card auth-card" onSubmit={submit} aria-labelledby="accept-title" noValidate>
        <div className="auth-head">
          <BrandMark size={40} />
          <h1 id="accept-title">Join Mirage</h1>
          <p className="muted">Choose a password for your new account.</p>
        </div>
        <div className="field">
          <label htmlFor="accept-password">Password</label>
          <input
            ref={passwordField}
            id="accept-password"
            type="password"
            autoComplete="new-password"
            required
            aria-invalid={problem !== null && password.length < minLength}
            aria-describedby="accept-password-rule"
            value={password}
            onChange={(e) => { setPassword(e.target.value); setProblem(null); }}
          />
          <span id="accept-password-rule" className={`rule${password.length >= minLength ? " rule-met" : ""}`}>
            {password.length >= minLength ? "✓ " : ""}At least {minLength} characters ({password.length}/{minLength})
          </span>
        </div>
        <div className="field">
          <label htmlFor="accept-confirm">Confirm password</label>
          <input
            id="accept-confirm"
            type="password"
            autoComplete="new-password"
            required
            aria-invalid={problem !== null && password.length >= minLength && password !== confirm}
            value={confirm}
            onChange={(e) => { setConfirm(e.target.value); setProblem(null); }}
          />
        </div>
        <div className="form-actions">
          {(problem ?? accept.isError) && <Alert>{problem ?? acceptError(accept.error)}</Alert>}
          <button type="submit" className="button button-primary" disabled={accept.isPending}>
            {accept.isPending ? "Creating account…" : "Create account"}
          </button>
        </div>
        <p className="muted small auth-foot">
          Already have an account? <Link to="/login">Sign in</Link>
        </p>
      </form>
    </main>
  );
}
