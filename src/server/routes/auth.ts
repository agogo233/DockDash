import type { NextFunction, Request, Response } from "express";
import { Router } from "express";
import { rateLimit } from "express-rate-limit";
import {
  authorizationCodeGrant,
  buildAuthorizationUrl,
  calculatePKCECodeChallenge,
  randomPKCECodeVerifier,
  randomState,
} from "openid-client";

import { passwordLoginRequestSchema } from "@shared/requestSchemas.js";

import { config } from "../lib/config.js";
import { logger } from "../lib/logService.js";
import { oidcService } from "../services/oidcService.js";

function callbackUrl(req: Request): string {
  // Explicit override takes precedence for unusual proxy setups
  if (config.oidcRedirectUri) return config.oidcRedirectUri;

  return `${req.protocol}://${req.get("host")}/auth/callback`;
}

// Extend express-session with OIDC and user data
declare module "express-session" {
  interface SessionData {
    user?: {
      sub: string;
      name?: string;
      email?: string;
      picture?: string;
    };
    authenticated?: boolean;
    oidcState?: string;
    oidcCodeVerifier?: string;
  }
}

const router = Router();
const authorizationRateLimit = rateLimit({
  windowMs: 15 * 60 * 1000,
  limit: 20,
  standardHeaders: "draft-8",
  legacyHeaders: false,
  message: { error: "Too many authentication attempts, please try again later" },
});

// Strict, IP-scoped limiter for the password endpoint. Combined with the
// 20/15min router-wide limiter, the effective cap is 5 attempts per 15 minutes.
const passwordLoginRateLimit = rateLimit({
  windowMs: 15 * 60 * 1000,
  limit: 5,
  standardHeaders: "draft-8",
  legacyHeaders: false,
  message: { error: "Too many authentication attempts, please try again later" },
});

const PASSWORD_LOGIN_FAILURE_DELAY_MS = 500;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// Reject browser requests initiated from a different site so cross-site login
// attempts (login CSRF) cannot ride on a victim's origin. Non-browser clients
// send neither header and pass through, matching the /api guard.
function sameOriginGuard(req: Request, res: Response, next: NextFunction): void {
  const site = req.get("sec-fetch-site");

  if (site === "cross-site") {
    res.status(403).json({ error: "Cross-site request blocked" });

    return;
  }

  if (site === undefined) {
    const origin = req.get("origin");

    if (origin !== undefined) {
      const expectedHost = req.get("x-forwarded-host") ?? req.get("host");
      const expected = `${req.protocol}://${expectedHost}`;

      if (origin !== expected) {
        res.status(403).json({ error: "Cross-origin request blocked" });

        return;
      }
    }
  }

  next();
}

router.use(authorizationRateLimit);

// Returns current auth state — always 200 so the client can bootstrap without error handling
router.get("/me", (req, res) => {
  const mode = config.authMode;

  if (mode === "none") {
    res.json({ enabled: false, mode, user: null });

    return;
  }

  if (mode === "password") {
    const user = req.session?.authenticated
      ? { sub: "local", name: config.read("authUsername") ?? undefined }
      : null;

    res.json({ enabled: true, mode, user });

    return;
  }

  res.json({ enabled: true, mode, user: req.session.user ?? null });
});

// Redirect to OIDC provider
router.get("/login", async (req, res) => {
  if (!config.oidcEnabled) {
    res.redirect("/");

    return;
  }

  try {
    const oidcConfig = await oidcService.getConfig();
    const codeVerifier = randomPKCECodeVerifier();
    const codeChallenge = await calculatePKCECodeChallenge(codeVerifier);
    const state = randomState();

    req.session.oidcCodeVerifier = codeVerifier;
    req.session.oidcState = state;

    const authUrl = buildAuthorizationUrl(oidcConfig, {
      scope: config.oidcScopes,
      redirect_uri: callbackUrl(req),
      code_challenge: codeChallenge,
      code_challenge_method: "S256",
      state,
    });

    res.redirect(authUrl.href);
  } catch (err) {
    logger.error(`OIDC login error: ${err instanceof Error ? err.message : String(err)}`);
    res.status(500).json({ error: "OIDC configuration error" });
  }
});

// Handle redirect back from OIDC provider
router.get("/callback", async (req, res) => {
  if (!req.session.oidcCodeVerifier || !req.session.oidcState) {
    res.redirect("/login?error=invalid_state");

    return;
  }

  try {
    const oidcConfig = await oidcService.getConfig();
    const currentUrl = new URL(req.originalUrl, callbackUrl(req));

    const tokenSet = await authorizationCodeGrant(oidcConfig, currentUrl, {
      pkceCodeVerifier: req.session.oidcCodeVerifier,
      expectedState: req.session.oidcState,
    });

    const claims = tokenSet.claims();

    if (!claims) throw new Error("No ID token claims in response");

    // Regenerate the session ID on login to prevent session fixation, then
    // explicitly save before redirecting so the new cookie reaches the client.
    await new Promise<void>((resolve, reject) => {
      req.session.regenerate((err) => (err ? reject(err) : resolve()));
    });

    req.session.user = {
      sub: claims.sub,
      name: typeof claims.name === "string" ? claims.name : undefined,
      email: typeof claims.email === "string" ? claims.email : undefined,
      picture: typeof claims.picture === "string" ? claims.picture : undefined,
    };

    await new Promise<void>((resolve, reject) => {
      req.session.save((err) => (err ? reject(err) : resolve()));
    });

    res.redirect("/");
  } catch (err) {
    logger.error(`OIDC callback error: ${err instanceof Error ? err.message : String(err)}`);
    res.redirect("/login?error=callback_failed");
  }
});

// Destroy session and redirect to login page
router.post("/logout", (req, res) => {
  req.session.destroy(() => {
    res.json({ ok: true });
  });
});

// Authenticate with a locally configured username and password
router.post("/password/login", sameOriginGuard, passwordLoginRateLimit, async (req, res) => {
  if (!config.passwordAuthEnabled) {
    res.status(403).json({ error: "Password authentication is not enabled" });

    return;
  }

  const parsed = passwordLoginRequestSchema.safeParse(req.body);

  if (!parsed.success) {
    res.status(400).json({ error: "Invalid request" });

    return;
  }

  const { username, password } = parsed.data;

  if (!config.verifyPassword(username, password)) {
    const safeUsername = username.replace(/[\r\n\t]/g, "");

    logger.warn(`[Auth] password login failed for "${safeUsername}" from ${req.ip}`);
    await sleep(PASSWORD_LOGIN_FAILURE_DELAY_MS);
    res.status(401).json({ error: "Invalid username or password" });

    return;
  }

  // Regenerate the session ID on login to prevent session fixation, then
  // explicitly save before responding so the new cookie reaches the client.
  await new Promise<void>((resolve, reject) => {
    req.session.regenerate((err) => (err ? reject(err) : resolve()));
  });

  req.session.authenticated = true;
  req.session.user = { sub: "local", name: config.read("authUsername") ?? undefined };

  await new Promise<void>((resolve, reject) => {
    req.session.save((err) => (err ? reject(err) : resolve()));
  });

  res.json({ ok: true });
});

export default router;
