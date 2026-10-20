export type Role = "admin" | "member";

export interface User {
  id: number;
  email: string;
  role: Role;
}

export type AuthType = "none" | "bearer" | "header";

export type ServerStatus = "connected" | "connecting" | "unreachable" | "disabled";

export interface Tool {
  name: string;
  description: string;
}

export interface Server {
  slug: string;
  url: string;
  enabled: boolean;
  auth_type: AuthType;
  auth_header?: string;
  has_credential: boolean;
  status: ServerStatus;
  reason?: string;
  tools: Tool[];
  created_at: string;
}

export interface NewServer {
  slug: string;
  url: string;
  auth_type: AuthType;
  auth_header?: string;
  credential?: string;
}

export interface ServerPatch {
  url?: string;
  enabled?: boolean;
  auth_type?: AuthType;
  auth_header?: string;
  credential?: string;
}

export interface Member extends User {
  created_at: string;
}

export interface Invite {
  id: string;
  email: string;
  role: Role;
  expires_at: string;
}

export interface CreatedInvite extends Invite {
  token: string;
}

export interface ApiKey {
  id: string;
  user_email: string;
  created_at: string;
  revoked_at: string | null;
  last_used_at: string | null;
}

export interface CreatedKey {
  id: string;
  user_email: string;
  created_at: string;
  key: string;
}

export type Outcome = "ok" | "tool_error" | "timeout" | "unreachable" | "canceled";

export const outcomes: readonly Outcome[] = ["ok", "tool_error", "timeout", "unreachable", "canceled"];

export interface AuditEntry {
  id: number;
  user_email: string;
  server: string;
  tool: string;
  started_at: string;
  duration_ms: number;
  outcome: Outcome;
  error?: string;
}

export interface AuditPage {
  entries: AuditEntry[];
  next_before?: number;
}

export interface AuditFilter {
  server: string;
  tool: string;
  outcome: Outcome | "";
  // 0 means every user; the API ignores it for members, who only ever see their own calls.
  userId: number;
}

export interface AuditOptions {
  servers: { slug: string; tools: string[] }[];
  users: { id: number; email: string }[];
}

export interface ChatMessage {
  role: "user" | "assistant";
  content: string;
}

export type ChatEvent =
  | { type: "tool_call"; id: string; name: string; arguments: unknown }
  | { type: "tool_result"; id: string; name: string; is_error: boolean; content: string }
  | { type: "answer"; text: string }
  | { type: "error"; message: string }
  | { type: "done" };

export const roles: readonly Role[] = ["admin", "member"];

export const authTypes: readonly AuthType[] = ["none", "bearer", "header"];

export function oneOf<T extends string>(options: readonly T[], value: string): T | undefined {
  return options.find((option) => option === value);
}
