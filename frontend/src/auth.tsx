import { createContext, useContext, useEffect, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "./lib/api";
import type { User } from "./lib/types";

interface AuthState {
  user: User | null;
  loading: boolean;
  refresh: () => Promise<unknown>;
  logout: () => Promise<void>;
}

const AuthCtx = createContext<AuthState>({ user: null, loading: true, refresh: async () => {}, logout: async () => {} });

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      try {
        return await api.get<User>("/auth/me");
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) return null;
        throw e;
      }
    },
    staleTime: 5 * 60_000,
  });

  useEffect(() => {
    const onExpired = () => qc.setQueryData(["me"], null);
    window.addEventListener("session-expired", onExpired);
    return () => window.removeEventListener("session-expired", onExpired);
  }, [qc]);

  const value: AuthState = {
    user: me.data ?? null,
    loading: me.isLoading,
    refresh: () => me.refetch(),
    logout: async () => {
      await api.post("/auth/logout").catch(() => {});
      // Update "me" in place (its observer stays attached), then drop the rest.
      // qc.clear() would detach the observer and leave the UI logged in.
      qc.setQueryData(["me"], null);
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
    },
  };
  return <AuthCtx.Provider value={value}>{children}</AuthCtx.Provider>;
}

export const useAuth = () => useContext(AuthCtx);
