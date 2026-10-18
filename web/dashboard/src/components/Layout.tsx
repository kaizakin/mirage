import { useMutation, useQueryClient } from "@tanstack/react-query";
import { NavLink, Outlet } from "react-router-dom";
import { api } from "../api/endpoints";
import { setSession, useCurrentUser } from "../session";

const pages = [
  { to: "/servers", label: "Servers", adminOnly: true },
  { to: "/members", label: "Members", adminOnly: true },
  { to: "/keys", label: "API keys", adminOnly: false },
  { to: "/audit", label: "Audit log", adminOnly: false },
  { to: "/chat", label: "Chat", adminOnly: false },
] as const;

export function Layout() {
  const user = useCurrentUser();
  const client = useQueryClient();
  const logout = useMutation({
    mutationFn: api.logout,
    onSettled: () => {
      setSession(client, null);
    },
  });

  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <aside className="sidebar">
        <div className="brand">Mirage</div>
        <nav aria-label="Main">
          <ul>
            {pages
              .filter((page) => !page.adminOnly || user.role === "admin")
              .map((page) => (
                <li key={page.to}>
                  <NavLink to={page.to} className="nav-link">
                    {page.label}
                  </NavLink>
                </li>
              ))}
          </ul>
        </nav>
      </aside>
      <div className="frame">
        <header className="topbar">
          <span className="who">
            <span className="who-email">{user.email}</span>
            <span className={`role role-${user.role}`}>{user.role}</span>
          </span>
          <button type="button" className="button button-quiet" disabled={logout.isPending} onClick={() => { logout.mutate(); }}>
            Log out
          </button>
        </header>
        <main id="main" tabIndex={-1} className="content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
