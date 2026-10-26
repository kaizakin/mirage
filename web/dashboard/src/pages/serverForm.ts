import type { AuthType, NewServer, Server, ServerPatch } from "../api/types";

/** Where a server comes from: an address the admin gives, or an image the platform runs. */
export type ServerSource = "url" | "image";

export interface ServerDraft {
  slug: string;
  source: ServerSource;
  url: string;
  image: string;
  port: string;
  path: string;
  /** KEY=value lines. When editing, blank keeps the stored environment. */
  env: string;
  authType: AuthType;
  authHeader: string;
  credential: string;
}

export const emptyDraft: ServerDraft = {
  slug: "",
  source: "url",
  url: "",
  image: "",
  port: "8080",
  path: "/mcp",
  env: "",
  authType: "none",
  authHeader: "",
  credential: "",
};

export const slugPattern = "[a-z0-9]+(-[a-z0-9]+)*";

export function draftFrom(server: Server): ServerDraft {
  return {
    ...emptyDraft,
    slug: server.slug,
    source: server.hosted ? "image" : "url",
    url: server.url,
    image: server.hosted?.image ?? "",
    authType: server.auth_type,
    authHeader: server.auth_header ?? "",
  };
}

function authFields(draft: ServerDraft): Pick<NewServer, "auth_type" | "auth_header" | "credential"> {
  const fields: Pick<NewServer, "auth_type" | "auth_header" | "credential"> = { auth_type: draft.authType };
  if (draft.authType === "header") fields.auth_header = draft.authHeader.trim();
  if (draft.authType !== "none" && draft.credential !== "") fields.credential = draft.credential;
  return fields;
}

/** Parses KEY=value lines, skipping blank ones; throws on a line without "=". */
export function parseEnv(text: string): Record<string, string> {
  const env: Record<string, string> = {};
  text.split("\n").forEach((line, i) => {
    if (line.trim() === "") return;
    const at = line.indexOf("=");
    if (at <= 0) throw new Error(`Environment line ${String(i + 1)} must read KEY=value.`);
    env[line.slice(0, at).trim()] = line.slice(at + 1);
  });
  return env;
}

export function toNewServer(draft: ServerDraft): NewServer {
  const base = { slug: draft.slug.trim(), ...authFields(draft) };
  if (draft.source === "url") return { ...base, url: draft.url.trim() };
  return {
    ...base,
    hosted: { image: draft.image.trim(), port: Number(draft.port), path: draft.path.trim(), env: parseEnv(draft.env) },
  };
}

export function toPatch(draft: ServerDraft): ServerPatch {
  if (draft.source === "url") return { url: draft.url.trim(), ...authFields(draft) };
  const hosted = draft.env.trim() === "" ? { image: draft.image.trim() } : { image: draft.image.trim(), env: parseEnv(draft.env) };
  return { hosted, ...authFields(draft) };
}
