import { useMutation, useQueryClient } from "@tanstack/react-query";
import { NavLink, Outlet } from "react-router-dom";
import { api } from "../api/endpoints";
import { setSession, useCurrentUser } from "../session";
import { BrandMark, Icon, type IconName } from "./Icon";

const pages: readonly { to: string; label: string; icon: IconName; adminOnly: boolean }[] = [
  { to: "/servers", label: "Servers", icon: "servers", adminOnly: true },
  { to: "/members", label: "Members", icon: "members", adminOnly: true },
  { to: "/keys", label: "API keys", icon: "key", adminOnly: false },
  { to: "/audit", label: "Audit log", icon: "audit", adminOnly: false },
  { to: "/chat", label: "Chat", icon: "chat", adminOnly: false },
];

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
        <div className="brand">
          <BrandMark />
          <div className="brand-text">
            <span className="brand-name">Mirage</span>
            <span className="brand-sub">MCP gateway</span>
          </div>
        </div>
        <nav aria-label="Main" className="nav">
          <ul>
            {pages
              .filter((page) => !page.adminOnly || user.role === "admin")
              .map((page) => (
                <li key={page.to}>
                  <NavLink to={page.to} className="nav-link">
                    <Icon name={page.icon} />
                    <span>{page.label}</span>
                  </NavLink>
                </li>
              ))}
          </ul>
        </nav>
        <div className="account">
          <span className="avatar" aria-hidden="true">
            {user.email.charAt(0).toUpperCase()}
          </span>
          <span className="account-text">
            <span className="account-email" title={user.email}>
              {user.email}
            </span>
            <span className={`role role-${user.role}`}>{user.role}</span>
          </span>
          <button
            type="button"
            className="icon-button"
            aria-label="Log out"
            title="Log out"
            disabled={logout.isPending}
            onClick={() => { logout.mutate(); }}
          >
            <Icon name="logout" />
          </button>
        </div>
      </aside>
      <main id="main" tabIndex={-1} className="content">
        <Outlet />
      </main>
    </div>
  );
}
