---
title: Authentication
description: Protect DockDash with built-in OIDC or an authenticated reverse proxy.
---

:::caution
DockDash does not enforce authentication by default. Configure authentication before exposing it to an untrusted network.
:::

## Password authentication

Enable a single local administrator account without an identity provider. It takes precedence over OIDC: when password authentication is enabled, any OIDC settings are ignored.

| Variable            | Default | Description                                                                    |
| ------------------- | ------- | ------------------------------------------------------------------------------ |
| `AUTH_USERNAME`     | unset   | Administrator username; enables password authentication when set with a secret |
| `AUTH_PASSWORD`     | unset   | Plaintext password. Use `AUTH_PASSWORD_HASH` instead where possible            |
| `AUTH_PASSWORD_HASH`| unset   | Scrypt hash, preferred over `AUTH_PASSWORD`. Format `scrypt$N$r$p$salt$hash`   |

Authentication is enabled when `AUTH_USERNAME` is set together with either `AUTH_PASSWORD` or `AUTH_PASSWORD_HASH`. Supplying only part of these settings fails startup so a misconfiguration can never silently leave the app unsecured. Invalid hash formats are rejected at startup.

Generate a hash with the built-in scrypt parameters (`N=16384, r=8, p=1, keylen=64`):

```bash
node -e "const c=require('crypto');const s=c.randomBytes(16).toString('hex');const h=c.scryptSync(process.argv[1],s,64,{N:16384,r:8,p:1}).toString('hex');console.log('scrypt$16384$8$1$'+s+'$'+h)" 'your-password'
```

```properties
AUTH_USERNAME=admin
AUTH_PASSWORD_HASH=scrypt$16384$8$1$<salt>$<hash>
SESSION_SECRET=replace-with-a-long-random-value
```

The login page posts to `/auth/password/login`. Failed attempts are rate-limited (5 per 15 minutes per IP), logged without the password, and delayed by 500 ms. A `SESSION_SECRET` is required so sessions survive restarts.

## Built-in OIDC

OIDC is enabled when all three required provider settings are present:

Startup fails if any OIDC setting is supplied without all three required provider settings containing non-blank values. The error names the missing settings without exposing their values. To disable built-in authentication for an authenticated reverse proxy, leave all OIDC environment variables unset; empty values also count as supplied settings.

| Variable             | Default                | Description                                                            |
| -------------------- | ---------------------- | ---------------------------------------------------------------------- |
| `OIDC_ISSUER`        | unset                  | Provider issuer or discovery URL                                       |
| `OIDC_CLIENT_ID`     | unset                  | Registered client ID                                                   |
| `OIDC_CLIENT_SECRET` | unset                  | Registered client secret                                               |
| `OIDC_REDIRECT_URI`  | auto-detected          | Explicit callback URL when proxy headers do not produce the public URL |
| `OIDC_SCOPES`        | `openid profile email` | Space-separated requested scopes                                       |
| `SESSION_SECRET`     | generated per process  | Cookie-signing secret; set a stable, random value in production        |
| `SESSION_MAX_AGE`    | `28800000`             | Session lifetime in milliseconds (eight hours)                         |

```properties
OIDC_ISSUER=https://auth.example.com/realms/homelab
OIDC_CLIENT_ID=dockdash
OIDC_CLIENT_SECRET=replace-with-provider-secret
SESSION_SECRET=replace-with-a-long-random-value
```

`OIDC_REDIRECT_URI` is normally inferred from the request. Set it explicitly when proxy headers do not describe the public URL correctly:

```properties
OIDC_REDIRECT_URI=https://dockdash.example.com/auth/callback
```

Register that exact callback URL with your identity provider.

:::tip
Set a stable `SESSION_SECRET` in production. If it is omitted, DockDash generates a new value at startup and existing sessions become invalid after every restart.
:::

## Authenticated reverse proxy

You can instead put DockDash behind Caddy, Traefik, nginx, oauth2-proxy, Authelia, Tailscale, or another authentication layer.

Prevent clients from bypassing the proxy by binding the published port to loopback:

```yaml
ports:
  - "127.0.0.1:3001:3001"
```

If the reverse proxy is on a separate Docker network, do not publish the DockDash port at all; connect the proxy directly to that network.

## Trusted proxy settings

DockDash defaults `TRUST_PROXY` to `loopback, uniquelocal`. Use `true` only when every upstream proxy is trusted. An overly broad setting can allow clients to spoof forwarded connection information.

Review [Security](../../security/) before enabling remote access.
