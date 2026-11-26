import type { NextFunction, Request, Response } from "express";
import { describe, expect, it, vi } from "vitest";

const mockConfig = vi.hoisted(() => ({ authMode: "none" as "none" | "oidc" | "password" }));

vi.mock("@server/lib/config.js", () => ({ config: mockConfig }));

const { requireAuth } = await import("@server/middleware/auth.js");

function makeReqRes(session: Record<string, unknown> = {}) {
  const req = { session } as unknown as Request;
  const res = {
    status: vi.fn().mockReturnThis(),
    json: vi.fn().mockReturnThis(),
  } as unknown as Response;
  const next = vi.fn() as unknown as NextFunction;

  return { req, res, next };
}

describe("requireAuth", () => {
  it("calls next() when authentication is disabled regardless of session state", () => {
    mockConfig.authMode = "none";
    const { req, res, next } = makeReqRes({ user: { sub: "user-1" } });

    requireAuth(req, res, next);
    expect(next).toHaveBeenCalledOnce();
    expect(res.status).not.toHaveBeenCalled();
  });

  it("calls next() when OIDC is enabled and the session has a user", () => {
    mockConfig.authMode = "oidc";
    const { req, res, next } = makeReqRes({
      user: { sub: "user-1", email: "user@example.com" },
    });

    requireAuth(req, res, next);
    expect(next).toHaveBeenCalledOnce();
    expect(res.status).not.toHaveBeenCalled();
  });

  it("returns 401 when OIDC is enabled but there is no session user", () => {
    mockConfig.authMode = "oidc";
    const { req, res, next } = makeReqRes();

    requireAuth(req, res, next);
    expect(next).not.toHaveBeenCalled();
    expect(res.status).toHaveBeenCalledWith(401);
    expect(res.json).toHaveBeenCalledWith({ error: "Unauthorized" });
  });

  it("calls next() when password auth is enabled and the session is authenticated", () => {
    mockConfig.authMode = "password";
    const { req, res, next } = makeReqRes({ authenticated: true });

    requireAuth(req, res, next);
    expect(next).toHaveBeenCalledOnce();
    expect(res.status).not.toHaveBeenCalled();
  });

  it("returns 401 when password auth is enabled but the session is not authenticated", () => {
    mockConfig.authMode = "password";
    const { req, res, next } = makeReqRes();

    requireAuth(req, res, next);
    expect(next).not.toHaveBeenCalled();
    expect(res.status).toHaveBeenCalledWith(401);
    expect(res.json).toHaveBeenCalledWith({ error: "Unauthorized" });
  });
});
