import type { NextFunction, Request, Response } from "express";

import { config } from "../lib/config.js";

export function requireAuth(req: Request, res: Response, next: NextFunction): void {
  const mode = config.authMode;

  if (mode === "none") {
    next();

    return;
  }

  if (mode === "oidc" && req.session?.user) {
    next();

    return;
  }

  if (mode === "password" && req.session?.authenticated) {
    next();

    return;
  }

  res.status(401).json({ error: "Unauthorized" });
}
