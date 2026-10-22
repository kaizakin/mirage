import { useQuery, type QueryClient } from "@tanstack/react-query";
import { createContext, use } from "react";
import { api } from "./api/endpoints";
import type { User } from "./api/types";

export const meKey = ["me"] as const;

export function useMe() {
  return useQuery({ queryKey: meKey, queryFn: api.me, staleTime: 60_000 });
}

export const UserContext = createContext<User | null>(null);

export function useCurrentUser(): User {
  const user = use(UserContext);
  if (!user) throw new Error("useCurrentUser must be rendered inside RequireUser.");
  return user;
}

export function setSession(client: QueryClient, user: User | null): void {
  client.setQueryData(meKey, user);
  client.removeQueries({ predicate: (query) => query.queryKey[0] !== meKey[0] });
  client.getMutationCache().clear();
}

// safeNext accepts only same-origin paths, so a crafted ?next= cannot send a signed-in user off-site.
// Browsers read "//host" and "/\host" as another origin.
export function safeNext(next: string | null): string | null {
  if (next === null || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return null;
  return next;
}

export function loginPath(next: string): string {
  return `/login?${new URLSearchParams({ next }).toString()}`;
}
