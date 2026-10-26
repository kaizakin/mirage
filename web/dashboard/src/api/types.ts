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
  /** The slug of the store that owns this server; its lifecycle, not the admin, manages it. */
  store?: string;
  /** Set when the platform runs the server from an image. */
  hosted?: HostedServer;
}

export type HostedStatus = "deploying" | "running" | "failed";

export interface HostedServer {
  image: string;
  port: number;
  path: string;
  status: HostedStatus;
  reason?: string;
}

/** Runs a server from an image. Port and path are fixed once it exists; env is write-only. */
export interface HostedInput {
  image?: string;
  port?: number;
  path?: string;
  env?: Record<string, string>;
}

export interface NewServer {
  slug: string;
  url?: string;
  hosted?: HostedInput;
  auth_type: AuthType;
  auth_header?: string;
  credential?: string;
}

export interface ServerPatch {
  url?: string;
  hosted?: HostedInput;
  enabled?: boolean;
  auth_type?: AuthType;
  auth_header?: string;
  credential?: string;
}

export type StoreStatus = "provisioning" | "ready" | "failed" | "deleting";

export interface Store {
  slug: string;
  name: string;
  status: StoreStatus;
  reason?: string;
  url: string;
  /** The store's MCP server slug, which prefixes its tools. */
  server: string;
  /** Empty when the owner's account was deleted. */
  owner_email: string;
  created_at: string;
  ready_at?: string;
}

export interface NewStore {
  name: string;
  slug: string;
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

/** The gateway either names the client asking for consent or says where to send the browser back to it. */
export type AuthorizeCheck = { client_name: string } | { redirect: string };

export interface AuthorizeDecision {
  redirect: string;
}

export interface ConnectedApp {
  id: number;
  client_name: string;
  created_at: string;
  last_used_at?: string;
}

export type Outcome = "ok" | "tool_error" | "timeout" | "unreachable" | "canceled" | "rate_limited" | "denied";

export const outcomes: readonly Outcome[] = ["ok", "tool_error", "timeout", "unreachable", "canceled", "rate_limited", "denied"];

/** A tool rule. user_id 0 covers every member; an empty server or tool covers every server or tool. */
export interface ToolRule {
  id: number;
  user_id: number;
  user_email?: string;
  server: string;
  tool: string;
  allow: boolean;
  created_at: string;
}

export type NewToolRule = Pick<ToolRule, "user_id" | "server" | "tool" | "allow">;

/** "removed" is a deleted server that still has calls in the health window. */
export type HealthStatus = ServerStatus | "removed";

export interface ServerHealth {
  server: string;
  status: HealthStatus;
  reason?: string;
  /** When the current status began; absent for disabled and removed servers. */
  since?: string;
  calls: number;
  failed: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  last_error?: string;
  last_outcome?: Outcome;
  last_error_at?: string;
}

export interface HealthReport {
  window_minutes: number;
  servers: ServerHealth[];
}

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
