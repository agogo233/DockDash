import express, { type RequestHandler } from "express";
import request from "supertest";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mockConfig = vi.hoisted(() => ({
  oidcEnabled: false,
  oidcRedirectUri: null as string | null,
  oidcScopes: "openid profile email",
  authMode: "none" as "none" | "oidc" | "password",
  passwordAuthEnabled: false,
  verifyPassword: vi.fn(() => false),
  read: vi.fn((key: string) => (key === "authUsername" ? "admin" : null)),
}));
const mockOidcService = vi.hoisted(() => ({ getConfig: vi.fn() }));
const mockLogger = vi.hoisted(() => ({ error: vi.fn(), warn: vi.fn() }));
const mockOpenid = vi.hoisted(() => ({
  authorizationCodeGrant: vi.fn(),
  buildAuthorizationUrl: vi.fn(),
  calculatePKCECodeChallenge: vi.fn(),
  randomPKCECodeVerifier: vi.fn(),
  randomState: vi.fn(),
}));

vi.mock("@server/lib/config.js", () => ({ config: mockConfig }));
vi.mock("@server/lib/logService.js", () => ({ logger: mockLogger }));
vi.mock("@server/services/oidcService.js", () => ({ oidcService: mockOidcService }));
vi.mock("openid-client", () => mockOpenid);

const { default: authRouter } = await import("@server/routes/auth.js");

type SessionCallbacks = {
  regenerateError?: Error;
  saveError?: Error;
};

function createApp(sessionData: Record<string, unknown> = {}, callbacks: SessionCallbacks = {}) {
  const app = express();
  const session: Record<string, unknown> & {
    regenerate: ReturnType<typeof vi.fn>;
    save: ReturnType<typeof vi.fn>;
    destroy: ReturnType<typeof vi.fn>;
  } = {
    ...sessionData,
    regenerate: vi.fn((callback: (error?: Error) => void) => callback(callbacks.regenerateError)),
    save: vi.fn((callback: (error?: Error) => void) => callback(callbacks.saveError)),
    destroy: vi.fn((callback: () => void) => callback()),
  };

  app.use(express.json());
  app.use(((req, _res, next) => {
    Object.defineProperty(req, "session", { value: session, writable: true });
    next();
  }) as RequestHandler);
  app.use("/auth", authRouter);

  return { app, session };
}

describe("auth routes", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockConfig.oidcEnabled = false;
    mockConfig.oidcRedirectUri = null;
    mockConfig.authMode = "none";
    mockConfig.passwordAuthEnabled = false;
    mockConfig.verifyPassword.mockReturnValue(false);
    mockOidcService.getConfig.mockResolvedValue({ serverMetadata: () => ({}) });
    mockOpenid.randomPKCECodeVerifier.mockReturnValue("verifier");
    mockOpenid.calculatePKCECodeChallenge.mockResolvedValue("challenge");
    mockOpenid.randomState.mockReturnValue("state");
    mockOpenid.buildAuthorizationUrl.mockReturnValue(new URL("https://idp.example/authorize"));
  });

  it("reports disabled authentication", async () => {
    const { app } = createApp();
    const response = await request(app).get("/auth/me");

    expect(response.status).toBe(200);
    expect(response.body).toEqual({ enabled: false, mode: "none", user: null });
  });

  it("reports the authenticated user when OIDC is enabled", async () => {
    mockConfig.authMode = "oidc";
    mockConfig.oidcEnabled = true;
    const user = { sub: "user-1", email: "user@example.com" };
    const { app } = createApp({ user });
    const response = await request(app).get("/auth/me");

    expect(response.body).toEqual({ enabled: true, mode: "oidc", user });
  });

  it("redirects disabled login attempts to the application", async () => {
    const { app } = createApp();
    const response = await request(app).get("/auth/login");

    expect(response.status).toBe(302);
    expect(response.headers.location).toBe("/");
  });

  it("creates PKCE state and redirects to the provider", async () => {
    mockConfig.oidcEnabled = true;
    mockConfig.oidcRedirectUri = "https://dockdash.example/auth/callback";
    const { app, session } = createApp();
    const response = await request(app).get("/auth/login");

    expect(response.status).toBe(302);
    expect(response.headers.location).toBe("https://idp.example/authorize");
    expect(session).toMatchObject({ oidcCodeVerifier: "verifier", oidcState: "state" });
    expect(mockOpenid.buildAuthorizationUrl).toHaveBeenCalledWith(
      expect.anything(),
      expect.objectContaining({
        redirect_uri: mockConfig.oidcRedirectUri,
        code_challenge: "challenge",
        code_challenge_method: "S256",
        state: "state",
      }),
    );
  });

  it("returns 500 when OIDC login setup fails", async () => {
    mockConfig.oidcEnabled = true;
    mockOidcService.getConfig.mockRejectedValue(new Error("discovery failed"));
    const { app } = createApp();
    const response = await request(app).get("/auth/login");

    expect(response.status).toBe(500);
    expect(response.body).toEqual({ error: "OIDC configuration error" });
    expect(mockLogger.error).toHaveBeenCalled();
  });

  it("rejects callbacks without stored PKCE state", async () => {
    const { app } = createApp();
    const response = await request(app).get("/auth/callback?code=abc");

    expect(response.status).toBe(302);
    expect(response.headers.location).toBe("/login?error=invalid_state");
  });

  it("regenerates and saves the session after a successful callback", async () => {
    const claims = {
      sub: "user-1",
      name: "Douglas",
      email: "douglas@example.com",
      picture: 123,
    };

    mockOpenid.authorizationCodeGrant.mockResolvedValue({ claims: () => claims });
    const { app, session } = createApp({
      oidcCodeVerifier: "verifier",
      oidcState: "state",
    });
    const response = await request(app)
      .get("/auth/callback?code=abc&state=state")
      .set("Host", "dockdash.example");

    expect(response.status).toBe(302);
    expect(response.headers.location).toBe("/");
    expect(session.regenerate).toHaveBeenCalledOnce();
    expect(session.save).toHaveBeenCalledOnce();
    expect(session.user).toEqual({
      sub: "user-1",
      name: "Douglas",
      email: "douglas@example.com",
      picture: undefined,
    });
  });

  it("redirects callback failures without exposing the error", async () => {
    mockOpenid.authorizationCodeGrant.mockResolvedValue({ claims: () => undefined });
    const { app } = createApp({ oidcCodeVerifier: "verifier", oidcState: "state" });
    const response = await request(app).get("/auth/callback?code=abc&state=state");

    expect(response.status).toBe(302);
    expect(response.headers.location).toBe("/login?error=callback_failed");
    expect(mockLogger.error).toHaveBeenCalled();
  });

  it("destroys the session on logout", async () => {
    const { app, session } = createApp({ user: { sub: "user-1" } });
    const response = await request(app).post("/auth/logout");

    expect(response.status).toBe(200);
    expect(response.body).toEqual({ ok: true });
    expect(session.destroy).toHaveBeenCalledOnce();
  });

  it("reports password mode with no user before login", async () => {
    mockConfig.authMode = "password";
    mockConfig.passwordAuthEnabled = true;
    const { app } = createApp();
    const response = await request(app).get("/auth/me");

    expect(response.status).toBe(200);
    expect(response.body).toEqual({ enabled: true, mode: "password", user: null });
  });

  it("reports the local user once authenticated in password mode", async () => {
    mockConfig.authMode = "password";
    mockConfig.passwordAuthEnabled = true;
    const { app } = createApp({ authenticated: true });
    const response = await request(app).get("/auth/me");

    expect(response.body).toEqual({
      enabled: true,
      mode: "password",
      user: { sub: "local", name: "admin" },
    });
  });

  it("rejects password login when password auth is disabled", async () => {
    const { app } = createApp();
    const response = await request(app)
      .post("/auth/password/login")
      .send({ username: "admin", password: "x" });

    expect(response.status).toBe(403);
    expect(response.body).toEqual({ error: "Password authentication is not enabled" });
  });

  it("rejects malformed login payloads", async () => {
    mockConfig.passwordAuthEnabled = true;
    const { app } = createApp();
    const response = await request(app).post("/auth/password/login").send({ username: "admin" });

    expect(response.status).toBe(400);
    expect(response.body).toEqual({ error: "Invalid request" });
  });

  it("logs and returns 401 on invalid credentials", async () => {
    mockConfig.authMode = "password";
    mockConfig.passwordAuthEnabled = true;
    mockConfig.verifyPassword.mockReturnValue(false);
    const { app } = createApp();
    const response = await request(app)
      .post("/auth/password/login")
      .send({ username: "admin", password: "bad" });

    expect(response.status).toBe(401);
    expect(response.body).toEqual({ error: "Invalid username or password" });
    expect(mockLogger.warn).toHaveBeenCalledWith(
      expect.stringContaining('password login failed for "admin"'),
    );
  });

  it("regenerates the session and authenticates on valid credentials", async () => {
    mockConfig.authMode = "password";
    mockConfig.passwordAuthEnabled = true;
    mockConfig.verifyPassword.mockReturnValue(true);
    const { app, session } = createApp();
    const response = await request(app)
      .post("/auth/password/login")
      .send({ username: "admin", password: "secret" });

    expect(response.status).toBe(200);
    expect(response.body).toEqual({ ok: true });
    expect(session.regenerate).toHaveBeenCalledOnce();
    expect(session.save).toHaveBeenCalledOnce();
    expect(session.authenticated).toBe(true);
    expect(session.user).toEqual({ sub: "local", name: "admin" });
  });

  it("blocks cross-site password login attempts", async () => {
    mockConfig.passwordAuthEnabled = true;
    const { app } = createApp();
    const response = await request(app)
      .post("/auth/password/login")
      .set("Origin", "https://evil.example")
      .send({ username: "admin", password: "x" });

    expect(response.status).toBe(403);
    expect(response.body).toEqual({ error: "Cross-origin request blocked" });
  });
});
