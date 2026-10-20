import { ApiError, call, json, send } from "./client";
import type {
  ApiKey,
  AuditFilter,
  AuditOptions,
  AuditPage,
  ChatMessage,
  CreatedInvite,
  CreatedKey,
  Invite,
  Member,
  NewServer,
  Role,
  Server,
  ServerPatch,
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

  members: () => json<Member[]>("GET", "/api/members"),
  setMemberRole: (id: number, role: Role) => call("PATCH", `/api/members/${id}`, { role }),
  removeMember: (id: number) => call("DELETE", `/api/members/${id}`),

  invites: () => json<Invite[]>("GET", "/api/invites"),
  createInvite: (email: string, role: Role) => json<CreatedInvite>("POST", "/api/invites", { email, role }),
  revokeInvite: (id: string) => call("DELETE", `/api/invites/${seg(id)}`),

  keys: (all: boolean) => json<ApiKey[]>("GET", all ? "/api/keys?all=1" : "/api/keys"),
  createKey: () => json<CreatedKey>("POST", "/api/keys"),
  revokeKey: (id: string) => call("DELETE", `/api/keys/${seg(id)}`),

  audit: (filter: AuditFilter, before: number): Promise<AuditPage> => {
    const params = new URLSearchParams({ limit: "50" });
    if (before > 0) params.set("before", String(before));
    if (filter.server) params.set("server", filter.server);
    if (filter.tool) params.set("tool", filter.tool);
    if (filter.outcome) params.set("outcome", filter.outcome);
    if (filter.userId > 0) params.set("user_id", String(filter.userId));
    return json<AuditPage>("GET", `/api/audit?${params.toString()}`);
  },
  auditOptions: () => json<AuditOptions>("GET", "/api/audit/options"),

  chat: (messages: ChatMessage[], signal: AbortSignal) => send("POST", "/api/chat", { messages }, signal),
};
