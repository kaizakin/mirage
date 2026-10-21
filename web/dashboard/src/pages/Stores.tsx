import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState, type SubmitEvent } from "react";
import { api } from "../api/endpoints";
import type { Store, StoreStatus } from "../api/types";
import { Badge, type Tone } from "../components/Badge";
import { ConfirmButton } from "../components/ConfirmButton";
import { Icon } from "../components/Icon";
import { Alert, EmptyState, ErrorNotice, Loading, MutationError } from "../components/States";
import { formatDate, formatRelative } from "../format";
import { useCurrentUser } from "../session";

const storesKey = ["stores"] as const;

const statusView: Record<StoreStatus, { label: string; tone: Tone; busy: boolean }> = {
  provisioning: { label: "Provisioning", tone: "amber", busy: true },
  ready: { label: "Ready", tone: "green", busy: false },
  failed: { label: "Failed", tone: "red", busy: false },
  deleting: { label: "Deleting", tone: "grey", busy: true },
};

const maxName = 60;
const maxSlug = 20;
const slugFormat = /^[a-z0-9]+(-[a-z0-9]+)*$/;
const defaultDomain = "shop.127.0.0.1.nip.io";

function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+/, "")
    .slice(0, maxSlug)
    .replace(/-+$/, "");
}

// The API builds a store's URL as http://<slug>.<domain>, so any existing store reveals the domain.
function storeDomain(stores: readonly Store[] | undefined): string {
  const store = stores?.[0];
  if (!store) return defaultDomain;
  const host = new URL(store.url).host;
  return host.startsWith(`${store.slug}.`) ? host.slice(store.slug.length + 1) : defaultDomain;
}

interface Problem {
  field: "name" | "slug";
  message: string;
}

function storeProblem(name: string, slug: string): Problem | null {
  if (name.trim() === "") return { field: "name", message: "Give the store a name." };
  if (name.trim().length > maxName) {
    return { field: "name", message: `The name can be at most ${maxName} characters.` };
  }
  if (slug === "") return { field: "slug", message: "Choose an address for the store." };
  if (slug.length > maxSlug || !slugFormat.test(slug)) {
    return {
      field: "slug",
      message: `The address must be 1 to ${maxSlug} lowercase letters, digits, or single hyphens, starting and ending with a letter or digit.`,
    };
  }
  return null;
}

function CreateStoreForm({ domain }: { domain: string }) {
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const nameField = useRef<HTMLInputElement>(null);
  const slugField = useRef<HTMLInputElement>(null);

  const create = useMutation({
    mutationFn: () => api.createStore({ name: name.trim(), slug }),
    onSuccess: async () => {
      setName("");
      setSlug("");
      setSlugEdited(false);
      await client.invalidateQueries({ queryKey: storesKey });
    },
  });

  const submit = (event: SubmitEvent) => {
    event.preventDefault();
    const found = storeProblem(name, slug);
    setProblem(found);
    if (found) {
      (found.field === "name" ? nameField : slugField).current?.focus();
      return;
    }
    create.mutate();
  };

  const shownSlug = slug || "your-store";
  return (
    <form className="card" onSubmit={submit} aria-labelledby="create-store-title" noValidate>
      <h2 id="create-store-title">Create a store</h2>
      <div className="form-grid">
        <div className="field">
          <label htmlFor="store-name">Name</label>
          <input
            ref={nameField}
            id="store-name"
            required
            maxLength={maxName}
            autoComplete="off"
            placeholder="Tea House"
            aria-invalid={problem?.field === "name"}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              if (!slugEdited) setSlug(slugify(e.target.value));
              setProblem(null);
              create.reset();
            }}
          />
        </div>
        <div className="field">
          <label htmlFor="store-slug">Address</label>
          <input
            ref={slugField}
            id="store-slug"
            required
            maxLength={maxSlug}
            autoComplete="off"
            spellCheck={false}
            placeholder="tea-house"
            aria-describedby="store-slug-preview"
            aria-invalid={problem?.field === "slug"}
            value={slug}
            onChange={(e) => {
              setSlug(e.target.value);
              setSlugEdited(e.target.value !== "");
              setProblem(null);
              create.reset();
            }}
          />
        </div>
      </div>
      <p id="store-slug-preview" className="hint store-preview">
        <span>
          Opens at <span className="mono">http://{shownSlug}.{domain}</span>
        </span>
        <span>
          Tools appear as <span className="mono">store-{shownSlug}__…</span>
        </span>
      </p>
      <div className="form-actions store-submit">
        {problem ? <Alert>{problem.message}</Alert> : <MutationError error={create.error} />}
        <button type="submit" className="button button-primary" disabled={create.isPending}>
          {create.isPending ? "Creating…" : "Create store"}
        </button>
      </div>
    </form>
  );
}

function When({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} title={formatDate(iso)}>
      {formatRelative(iso)}
    </time>
  );
}

function StoreCard({ store, showOwner }: { store: Store; showOwner: boolean }) {
  const client = useQueryClient();
  const refresh = () => client.invalidateQueries({ queryKey: storesKey });
  const retry = useMutation({
    mutationFn: () => api.createStore({ name: store.name, slug: store.slug }),
    onSuccess: refresh,
  });
  const remove = useMutation({ mutationFn: () => api.deleteStore(store.slug), onSuccess: refresh });
  const view = statusView[store.status];
  const ready = store.status === "ready";

  return (
    <li className={`server store-${store.status}`}>
      <div className="server-main">
        <div className="server-id">
          <h2 className="server-slug">{store.name}</h2>
          <span role="status">
            <Badge tone={view.tone} busy={view.busy}>
              {view.label}
            </Badge>
          </span>
        </div>
        <div className="server-url mono truncate" title={store.url}>
          {ready ? (
            <a href={store.url} target="_blank" rel="noopener noreferrer">
              {store.url}
              <Icon name="external" size={12} />
              <span className="visually-hidden"> (opens in a new tab)</span>
            </a>
          ) : (
            store.url
          )}
        </div>
        <div className="row-actions">
          {store.status === "failed" && (
            <button
              type="button"
              className="button button-quiet"
              aria-label={`Retry ${store.slug}`}
              disabled={retry.isPending}
              onClick={() => { retry.mutate(); }}
            >
              <Icon name="refresh" />
              {retry.isPending ? "Retrying…" : "Retry"}
            </button>
          )}
          <ConfirmButton
            label="Delete"
            subject={store.slug}
            confirmLabel={`Delete ${store.slug}`}
            disabled={store.status === "deleting"}
            busy={remove.isPending}
            onConfirm={() => { remove.mutate(); }}
          />
        </div>
      </div>
      {store.status === "failed" && store.reason && (
        <p className="reason">
          <Icon name="alert" size={14} />
          <span>{store.reason}</span>
        </p>
      )}
      <dl className="server-meta store-meta">
        <div>
          <dt>Tool prefix</dt>
          <dd className="mono">{store.server}__</dd>
        </div>
        {showOwner && (
          <div>
            <dt>Owner</dt>
            <dd className="truncate" title={store.owner_email}>
              {store.owner_email || <span className="muted">Deleted user</span>}
            </dd>
          </div>
        )}
        <div>
          <dt>Created</dt>
          <dd>
            <When iso={store.created_at} />
          </dd>
        </div>
        <div>
          <dt>Ready</dt>
          <dd>{store.ready_at ? <When iso={store.ready_at} /> : <span className="muted">Not yet</span>}</dd>
        </div>
      </dl>
      <MutationError error={retry.error ?? remove.error} />
    </li>
  );
}

function changing(stores: readonly Store[] | undefined): boolean {
  return stores?.some((store) => statusView[store.status].busy) ?? false;
}

export function StoresPage() {
  const user = useCurrentUser();
  const stores = useQuery({
    queryKey: storesKey,
    queryFn: api.stores,
    refetchInterval: (query) => (changing(query.state.data) ? 3000 : 15_000),
  });

  return (
    <section aria-labelledby="stores-title">
      <div className="page-head">
        <div>
          <h1 id="stores-title">Stores</h1>
          <p className="muted">
            Each store is a WooCommerce shop in its own namespace, with its own database and MCP server.
          </p>
        </div>
      </div>
      <CreateStoreForm domain={storeDomain(stores.data)} />
      {stores.isPending ? (
        <Loading />
      ) : stores.isError && !stores.data ? (
        <ErrorNotice error={stores.error} />
      ) : stores.data.length === 0 ? (
        <EmptyState icon="store" title="No stores yet">
          Create one above. It is ready to use in a few minutes.
        </EmptyState>
      ) : (
        <ul className="server-list" aria-label="Stores">
          {stores.data.map((store) => (
            <StoreCard key={store.slug} store={store} showOwner={user.role === "admin"} />
          ))}
        </ul>
      )}
    </section>
  );
}
