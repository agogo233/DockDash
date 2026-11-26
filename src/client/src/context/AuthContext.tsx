import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useState } from "react";

import { authApi } from "../services/api";

interface AuthUser {
  sub: string;
  name?: string;
  email?: string;
  picture?: string;
}

interface AuthState {
  enabled: boolean;
  mode: "none" | "oidc" | "password";
  user: AuthUser | null;
  isLoading: boolean;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState>({
  enabled: false,
  mode: "none",
  user: null,
  isLoading: true,
  logout: async () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const [enabled, setEnabled] = useState(false);
  const [mode, setMode] = useState<AuthState["mode"]>("none");
  const [user, setUser] = useState<AuthUser | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    void authApi
      .getState()
      .then((res) => {
        setEnabled(res.data.enabled);
        setMode(res.data.mode);
        setUser(res.data.user);
      })
      .catch(() => {
        setEnabled(false);
        setMode("none");
        setUser(null);
      })
      .finally(() => setIsLoading(false));
  }, []);

  const logout = async () => {
    await authApi.logout();
    setUser(null);
    window.location.href = "/login";
  };

  return (
    <AuthContext.Provider value={{ enabled, mode, user, isLoading, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export const useAuth = () => useContext(AuthContext);
