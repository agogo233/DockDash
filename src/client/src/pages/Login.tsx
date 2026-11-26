import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";

import { useAuth } from "../context/AuthContext";
import { authApi } from "../services/api";

function Login() {
  const { t } = useTranslation();
  const { enabled, mode, user, isLoading } = useAuth();
  const navigate = useNavigate();
  const error = new URLSearchParams(window.location.search).get("error");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  useEffect(() => {
    if (!isLoading && (!enabled || user)) {
      navigate("/", { replace: true });
    }
  }, [enabled, user, isLoading, navigate]);

  if (isLoading) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    if (submitting) return;

    if (!username || !password) {
      setFormError(t("login.empty"));

      return;
    }

    setSubmitting(true);
    setFormError(null);

    try {
      await authApi.passwordLogin(username, password);
      window.location.href = "/";
    } catch (err) {
      const status = (err as { response?: { status?: number } })?.response?.status;

      setFormError(status === 429 ? t("login.rateLimited") : t("login.invalidCredentials"));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <div className="flex flex-col items-center gap-6 p-10 rounded-xl border border-border bg-card shadow-lg w-full max-w-sm">
        <div className="flex flex-col items-center gap-2">
          <img src="/logo.svg" alt="" className="size-10" aria-hidden="true" />
          <h1 className="text-2xl font-bold">DockDash</h1>
        </div>

        {mode === "password" ? (
          <form onSubmit={handleSubmit} className="flex flex-col items-center gap-4 w-full">
            <p className="text-sm text-muted-foreground text-center">{t("login.signIn")}</p>

            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder={t("login.username")}
              autoComplete="username"
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm text-foreground outline-none focus:border-primary"
            />
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t("login.password")}
              autoComplete="current-password"
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm text-foreground outline-none focus:border-primary"
            />

            {formError && <p className="text-sm text-destructive text-center">{formError}</p>}

            <button
              type="submit"
              disabled={submitting}
              className="w-full px-4 py-2 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors cursor-pointer disabled:opacity-60"
            >
              {submitting ? t("login.submitting") : t("login.submit")}
            </button>
          </form>
        ) : (
          <>
            <p className="text-sm text-muted-foreground text-center">{t("login.signIn")}</p>

            {error && (
              <p className="text-sm text-destructive text-center">{t("login.authFailed")}</p>
            )}

            <a href="/auth/login" className="w-full no-underline">
              <button className="w-full px-4 py-2 rounded-md bg-primary text-primary-foreground text-sm font-medium hover:bg-primary/90 transition-colors cursor-pointer">
                {t("login.ssoLogin")}
              </button>
            </a>
          </>
        )}

        <a
          href="https://github.com/dougmaitelli/DockDash"
          target="_blank"
          rel="noreferrer"
          className="text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          {t("nav.sourceLicense")}
        </a>
      </div>
    </div>
  );
}

export default Login;
