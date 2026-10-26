import { authTypes, oneOf, type AuthType } from "../api/types";
import { slugPattern, type ServerDraft } from "./serverForm";
import { PasswordInput } from "../components/PasswordInput";

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
      {mode === "create" && (
        <div className="field">
          <label htmlFor={id("source")}>Source</label>
          <select id={id("source")} value={draft.source} onChange={(e) => { set("source", e.target.value === "image" ? "image" : "url"); }}>
            <option value="url">Connect to a URL</option>
            <option value="image">Run a container image</option>
          </select>
        </div>
      )}
      {draft.source === "url" ? (
        <div className="field">
          <label htmlFor={id("url")}>MCP URL</label>
          <input
            id={id("url")}
            type="url"
            required
            placeholder="https://mcp.example.com/mcp"
            aria-describedby={id("url-hint")}
            spellCheck={false}
            value={draft.url}
            onChange={(e) => { set("url", e.target.value); }}
          />
          <span id={id("url-hint")} className="hint">
            A public address. To run a server inside the cluster, host its image instead.
          </span>
        </div>
      ) : (
        <>
          <div className="field">
            <label htmlFor={id("image")}>Image</label>
            <input
              id={id("image")}
              required
              placeholder="ghcr.io/org/mcp-server:1.0"
              aria-describedby={id("image-hint")}
              spellCheck={false}
              value={draft.image}
              onChange={(e) => { set("image", e.target.value); }}
            />
            <span id={id("image-hint")} className="hint">
              Runs as a non-root user with a read-only filesystem, reachable only by the gateway.
            </span>
          </div>
          {mode === "create" && (
            <>
              <div className="field">
                <label htmlFor={id("port")}>Port</label>
                <input
                  id={id("port")}
                  type="number"
                  min={1}
                  max={65535}
                  required
                  value={draft.port}
                  onChange={(e) => { set("port", e.target.value); }}
                />
              </div>
              <div className="field">
                <label htmlFor={id("path")}>MCP path</label>
                <input
                  id={id("path")}
                  required
                  pattern="/.*"
                  spellCheck={false}
                  value={draft.path}
                  onChange={(e) => { set("path", e.target.value); }}
                />
              </div>
            </>
          )}
          <div className="field">
            <label htmlFor={id("env")}>{mode === "edit" ? "New environment" : "Environment"}</label>
            <textarea
              id={id("env")}
              rows={3}
              placeholder="API_KEY=…"
              aria-describedby={id("env-hint")}
              spellCheck={false}
              value={draft.env}
              onChange={(e) => { set("env", e.target.value); }}
            />
            <span id={id("env-hint")} className="hint">
              {mode === "edit"
                ? "One KEY=value per line. Leave blank to keep the stored environment."
                : "One KEY=value per line. Stored encrypted and never shown again."}
            </span>
          </div>
        </>
      )}
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
          <PasswordInput
            id={id("credential")}
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
