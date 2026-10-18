import type { AuthType, NewServer, Server, ServerPatch } from "../api/types";

export interface ServerDraft {
  slug: string;
  url: string;
  authType: AuthType;
  authHeader: string;
  credential: string;
}

export const emptyDraft: ServerDraft = { slug: "", url: "", authType: "none", authHeader: "", credential: "" };

export const slugPattern = "[a-z0-9]+(-[a-z0-9]+)*";

export function draftFrom(server: Server): ServerDraft {
  return {
    slug: server.slug,
    url: server.url,
    authType: server.auth_type,
    authHeader: server.auth_header ?? "",
    credential: "",
  };
}

function authFields(draft: ServerDraft): Pick<NewServer, "auth_type" | "auth_header" | "credential"> {
  const fields: Pick<NewServer, "auth_type" | "auth_header" | "credential"> = { auth_type: draft.authType };
  if (draft.authType === "header") fields.auth_header = draft.authHeader.trim();
  if (draft.authType !== "none" && draft.credential !== "") fields.credential = draft.credential;
  return fields;
}

export function toNewServer(draft: ServerDraft): NewServer {
  return { slug: draft.slug.trim(), url: draft.url.trim(), ...authFields(draft) };
}

export function toPatch(draft: ServerDraft): ServerPatch {
  return { url: draft.url.trim(), ...authFields(draft) };
}
