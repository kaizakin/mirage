import type { ReactNode } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { Layout } from "./components/Layout";
import { AdminsOnly, ErrorNotice, Loading } from "./components/States";
import { AcceptInvitePage } from "./pages/AcceptInvite";
import { AuditPage } from "./pages/Audit";
import { AuthorizePage } from "./pages/Authorize";
import { ChatPage } from "./pages/Chat";
import { KeysPage } from "./pages/Keys";
import { LoginPage } from "./pages/Login";
import { MembersPage } from "./pages/Members";
import { ServersPage } from "./pages/Servers";
import { StoresPage } from "./pages/Stores";
import { loginPath, UserContext, useCurrentUser, useMe } from "./session";

function RequireUser({ children }: { children: ReactNode }) {
  const me = useMe();
  const location = useLocation();
  if (me.isPending) return <Loading />;
  if (me.isError) return <ErrorNotice error={me.error} />;
  if (me.data === null) return <Navigate to={loginPath(location.pathname + location.search)} replace />;
  return <UserContext value={me.data}>{children}</UserContext>;
}

function RequireAdmin({ children }: { children: ReactNode }) {
  return useCurrentUser().role === "admin" ? children : <AdminsOnly />;
}

function Home() {
  return <Navigate to={useCurrentUser().role === "admin" ? "/servers" : "/keys"} replace />;
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/accept" element={<AcceptInvitePage />} />
      <Route path="/oauth/authorize" element={<AuthorizePage />} />
      <Route
        element={
          <RequireUser>
            <Layout />
          </RequireUser>
        }
      >
        <Route index element={<Home />} />
        <Route
          path="servers"
          element={
            <RequireAdmin>
              <ServersPage />
            </RequireAdmin>
          }
        />
        <Route
          path="members"
          element={
            <RequireAdmin>
              <MembersPage />
            </RequireAdmin>
          }
        />
        <Route path="stores" element={<StoresPage />} />
        <Route path="keys" element={<KeysPage />} />
        <Route path="audit" element={<AuditPage />} />
        <Route path="chat" element={<ChatPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
