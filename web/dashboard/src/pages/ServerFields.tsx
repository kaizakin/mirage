import { authTypes, oneOf, type AuthType } from "../api/types";
import { slugPattern, type ServerDraft } from "./serverForm";

const authLabels: Record<AuthType, string> = {
  none: "None",
  bearer: "Bearer token",
  header: "Custom header",
};

interface Props {
  idPrefix: string;
  draft: ServerDraft;
  onChange: (draft: ServerDraft) => void;
  mode: "create" | "edit";
  hasCredential?: boolean;
}

export function ServerFields({ idPrefix, draft, onChange, mode, hasCredential = false }: Props) {
  const set = <K extends keyof ServerDraft>(key: K, value: ServerDraft[K]) => {
    onChange({ ...draft, [key]: value });
  };
  const id = (name: string) => `${idPrefix}-${name}`;

  return (
    <div className="form-grid">
      {mode === "create" && (
        <div className="field">
          <label htmlFor={id("slug")}>Slug</label>
          <input
            id={id("slug")}
            required
            pattern={slugPattern}
            aria-describedby={id("slug-hint")}
            autoComplete="off"
            spellCheck={false}
            value={draft.slug}
            onChange={(e) => { set("slug", e.target.value); }}
          />
          <span id={id("slug-hint")} className="hint">
            Lowercase letters, digits, and single hyphens. Tools appear as slug__tool.
          </span>
        </div>
      )}
      <div className="field">
        <label htmlFor={id("url")}>MCP URL</label>
        <input
          id={id("url")}
          type="url"
          required
          placeholder="http://service.namespace.svc:8080/mcp"
          spellCheck={false}
          value={draft.url}
          onChange={(e) => { set("url", e.target.value); }}
        />
      </div>
      <div className="field">
        <label htmlFor={id("auth")}>Authentication</label>
        <select id={id("auth")} value={draft.authType} onChange={(e) => {
            const type = oneOf(authTypes, e.target.value);
            if (type) set("authType", type);
          }}>
          {authTypes.map((type) => (
            <option key={type} value={type}>
              {authLabels[type]}
            </option>
          ))}
        </select>
      </div>
      {draft.authType === "header" && (
        <div className="field">
          <label htmlFor={id("header")}>Header name</label>
          <input
            id={id("header")}
            required
            placeholder="X-API-Key"
            spellCheck={false}
            value={draft.authHeader}
            onChange={(e) => { set("authHeader", e.target.value); }}
          />
        </div>
      )}
      {draft.authType !== "none" && (
        <div className="field">
          <label htmlFor={id("credential")}>{mode === "edit" ? "New credential" : "Credential"}</label>
          <input
            id={id("credential")}
            type="password"
            autoComplete="new-password"
            required={mode === "create" || !hasCredential}
            aria-describedby={id("credential-hint")}
            value={draft.credential}
            onChange={(e) => { set("credential", e.target.value); }}
          />
          <span id={id("credential-hint")} className="hint">
            {mode === "edit" && hasCredential
              ? "A credential is stored. Leave blank to keep it."
              : "Stored encrypted and never shown again."}
          </span>
        </div>
      )}
    </div>
  );
}
