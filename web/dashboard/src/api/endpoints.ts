import { ApiError, call, json, send } from "./client";
import type {
  ApiKey,
  AuditFilter,
  AuditOptions,
  AuditPage,
  AuthorizeCheck,
  AuthorizeDecision,
  ChatMessage,
  ConnectedApp,
  CreatedInvite,
  CreatedKey,
  HealthReport,
  Invite,
  Member,
  NewServer,
  NewStore,
  Role,
  Server,
  ServerPatch,
  Store,
  User,
} from "./types";

const seg = encodeURIComponent;

export const api = {
  me: async (): Promise<User | null> => {
    try {
      return await json<User>("GET", "/api/me");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) return null;
      throw error;
    }
  },
  login: (email: string, password: string) => json<User>("POST", "/api/login", { email, password }),
  logout: () => call("POST", "/api/logout"),
  acceptInvite: (token: string, password: string) => json<User>("POST", "/api/invites/accept", { token, password }),

  servers: () => json<Server[]>("GET", "/api/servers"),
  createServer: (server: NewServer) => call("POST", "/api/servers", server),
  updateServer: (slug: string, patch: ServerPatch) => call("PATCH", `/api/servers/${seg(slug)}`, patch),
  deleteServer: (slug: string) => call("DELETE", `/api/servers/${seg(slug)}`),

  stores: () => json<Store[]>("GET", "/api/stores"),
  createStore: (store: NewStore) => json<Store>("POST", "/api/stores", store),
  deleteStore: (slug: string) => call("DELETE", `/api/stores/${seg(slug)}`),

  members: () => json<Member[]>("GET", "/api/members"),
  setMemberRole: (id: number, role: Role) => call("PATCH", `/api/members/${id}`, { role }),
  removeMember: (id: number) => call("DELETE", `/api/members/${id}`),

  invites: () => json<Invite[]>("GET", "/api/invites"),
  createInvite: (email: string, role: Role) => json<CreatedInvite>("POST", "/api/invites", { email, role }),
  revokeInvite: (id: string) => call("DELETE", `/api/invites/${seg(id)}`),

  keys: (all: boolean) => json<ApiKey[]>("GET", all ? "/api/keys?all=1" : "/api/keys"),
  createKey: () => json<CreatedKey>("POST", "/api/keys"),
  revokeKey: (id: string) => call("DELETE", `/api/keys/${seg(id)}`),

  checkAuthorize: (query: string) => json<AuthorizeCheck>("GET", `/api/oauth/authorize?${query}`),
  decideAuthorize: (query: string, approve: boolean) =>
    json<AuthorizeDecision>("POST", "/api/oauth/authorize", { query, approve }),
  connectedApps: () => json<ConnectedApp[]>("GET", "/api/oauth/apps"),
  revokeConnectedApp: (id: number) => call("DELETE", `/api/oauth/apps/${id}`),

  audit: (filter: AuditFilter, before: number): Promise<AuditPage> => {
    const params = new URLSearchParams({ limit: "50" });
    if (before > 0) params.set("before", String(before));
    if (filter.server) params.set("server", filter.server);
    if (filter.tool) params.set("tool", filter.tool);
    if (filter.outcome) params.set("outcome", filter.outcome);
    if (filter.userId > 0) params.set("user_id", String(filter.userId));
    return json<AuditPage>("GET", `/api/audit?${params.toString()}`);
  },
  health: () => json<HealthReport>("GET", "/api/health"),

  auditOptions: () => json<AuditOptions>("GET", "/api/audit/options"),

  chat: (messages: ChatMessage[], signal: AbortSignal) => send("POST", "/api/chat", { messages }, signal),
};
