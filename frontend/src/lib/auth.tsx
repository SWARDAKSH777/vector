import React, {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
} from "react";
import { APIError, api, clearAPICache, type AuthUser } from "../lib/api";

interface AuthCtx {
  user: AuthUser | null;
  loading: boolean;
  error: string;
  retry: () => Promise<void>;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const Ctx = createContext<AuthCtx>({
  user: null,
  loading: true,
  error: "",
  retry: async () => {},
  login: async () => {},
  logout: async () => {},
});

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const retry = useCallback(async () => {
    setLoading(true);
    setError("");
    clearAPICache(["/api/auth/me"]);
    try {
      setUser(await api.me());
    } catch (reason: unknown) {
      if (reason instanceof APIError && reason.status === 401) {
        setUser(null);
      } else {
        setError(reason instanceof Error ? reason.message : "Could not check your session");
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void retry();
  }, [retry]);

  const login = useCallback(async (email: string, password: string) => {
    await api.login(email, password);
    const me = await api.me();
    setError("");
    setUser(me);
  }, []);

  const logout = useCallback(async () => {
    await api.logout();
    setError("");
    setUser(null);
  }, []);

  return (
    <Ctx.Provider value={{ user, loading, error, retry, login, logout }}>
      {children}
    </Ctx.Provider>
  );
}

export const useAuth = () => useContext(Ctx);
