// Minimal auth context backed by the backend JWT stored in localStorage.

import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { loadToken, logout as backendLogout, setToken } from "./backend";
import { clearCredentials } from "./credentials";

type AuthCtx = {
  token: string | null;
  isAuthed: boolean;
  signIn: (token: string) => void;
  signOut: () => void;
};

const Ctx = createContext<AuthCtx>({
  token: null,
  isAuthed: false,
  signIn: () => {},
  signOut: () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setTok] = useState<string | null>(null);

  useEffect(() => {
    setTok(loadToken());
  }, []);

  const signIn = (t: string) => {
    // Drop any prior account's cached keys/settings before switching accounts.
    clearCredentials();
    setToken(t);
    setTok(t);
  };
  const signOut = () => {
    backendLogout();
    clearCredentials();
    setTok(null);
  };

  return (
    <Ctx.Provider value={{ token, isAuthed: !!token, signIn, signOut }}>
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  return useContext(Ctx);
}
