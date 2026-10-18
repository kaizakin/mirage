export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export function isStatus(error: unknown, status: number): boolean {
  return error instanceof ApiError && error.status === status;
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Something went wrong.";
}

type Method = "GET" | "POST" | "PATCH" | "DELETE";

export async function send(method: Method, path: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method, credentials: "same-origin", headers };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  if (signal) init.signal = signal;
  const res = await fetch(path, init);
  if (!res.ok) throw new ApiError(res.status, await readError(res));
  return res;
}

async function readError(res: Response): Promise<string> {
  try {
    const parsed: unknown = await res.json();
    if (typeof parsed === "object" && parsed !== null && "error" in parsed && typeof parsed.error === "string") {
      return parsed.error;
    }
  } catch {
    // Fall through to the status text when the body is not JSON, as with a proxy error page.
  }
  return res.statusText || `Request failed with status ${res.status}`;
}

// The gateway's JSON shapes are fixed by the contract in src/api/types.ts; this is the one place we trust them.
export async function json<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const res = await send(method, path, body);
  const parsed: unknown = await res.json();
  return parsed as T;
}

export async function call(method: Method, path: string, body?: unknown): Promise<void> {
  await send(method, path, body);
}
