# ADR 001: Trust the Tailscale login from `tailscale serve`

- **Status:** accepted
- **Date:** 2026-11-18

## Context

jobtrack holds a personal job search: companies, contacts, notes, salaries discussed. It
runs on a Raspberry Pi at home that is only reachable over my tailnet, with one user (me)
and maybe a friend or two later. It needs to know *who* is calling and turn everyone else
away, without making me log in on each device or run an identity provider on a 4 GB Pi.

Every device that can reach the Pi is already signed in to Tailscale. When `tailscale serve`
proxies a request to a local service, it adds the caller's identity in headers:

- `Tailscale-User-Login`, e.g. `you@github`; non-ASCII values may be RFC 2047 encoded
- `Tailscale-User-Name`, the display name
- `Tailscale-User-Profile-Pic`, a URL

Before setting these headers, serve deletes any copy the client sent, under any spelling
(it matches case-insensitively and treats `_` as `-`). It sets no identity headers for
requests from tagged devices or from Funnel (public) traffic.

## Decision

The API binds to `127.0.0.1` only and is reached through `tailscale serve`. A middleware,
`requireLogin` in `internal/httpapi/auth.go`, handles every request:

- **No login:** 401 `unauthenticated`. This happens when the request didn't come through
  serve, or came from a tagged device or through Funnel.
- **Login not on the allowlist:** 403 `forbidden`. The allowlist is
  `JOBTRACK_ALLOWED_LOGINS`, compared ignoring case.
- **Otherwise:** the request goes through. The web app is behind the check too, not just
  `/api`.
- **Health probes stay open:** `GET /healthz` and `/readyz` need no login, because deploy
  checks, Docker healthchecks and Uptime Kuma call them directly. They reveal only the
  build version and whether the database is up.
- **Metrics aren't an exception:** `/metrics` (step 5.1) is served on a separate address,
  `JOBTRACK_METRICS_ADDR`, that tailscale serve doesn't proxy, so Prometheus can scrape
  it without a login and the tailnet can't see it. On the app's address it's an ordinary
  path, behind the login check like everything else.

The middleware sits inside the access log, so refused requests are logged, with the login
that was refused.

It fails closed:

- The zero-value `Auth` lets nobody in.
- The server won't start without an allowlist.
- `JOBTRACK_AUTH=off`, which skips the check for local development, is refused unless the
  server listens on a loopback address.

## Consequences

**Good**

- No passwords, sessions, cookies or CSRF tokens to build, store or get wrong. Tailscale
  already did the authentication: device keys, SSO with the identity provider, and MFA if
  enabled.
- Works the same from my phone and laptop with no login screen.
- Small, and testable with `httptest`: the tests play the role of serve by setting the
  header.

**Risks, and what limits them**

- **Anything that can reach the port directly can forge the header.** That's the core
  trade-off.
  - Binding to `127.0.0.1` limits this to processes on the Pi itself.
  - In production the API runs in a container. Its port is published on `127.0.0.1`
    only, and the containers on its Docker network are mine (Postgres; later
    Prometheus).
  - No container gets the Docker socket, and nothing on the Pi runs untrusted code.
  - A compromised local process could forge the header, but it could also read the
    database files.
- **Misconfigured exposure.** If someone binds to `0.0.0.0` or points Funnel at the app,
  the header check still holds: Funnel requests carry no login, so they get 401. Binding
  to a LAN address, though, would let LAN clients forge the header. The deploy compose
  file pins `127.0.0.1:8090`, and `JOBTRACK_AUTH=off` can't be combined with a
  non-loopback address.
- **Tagged devices are always refused**, because serve sends no identity for them. That's
  fine: all my devices are personal.
- **Coupled to Tailscale.** Moving off it means replacing this middleware (see below).
  Handlers don't read the header, so the change stays in one place.

## Alternatives considered

- **Tailscale ACLs only, no app check.** Simpler, but any device in the tailnet, including
  a shared or tagged one, would get full access. The app should still say who is allowed.
- **Own login (password + session cookie).** Adds password storage, session expiry, CSRF
  protection and a login page. That's more code to get wrong, for one user who is already
  authenticated by Tailscale.
- **OIDC through an identity provider (e.g. Authelia, or GitHub OAuth via oauth2-proxy).**
  The right shape for a multi-user product. Here it's another service to run and patch on
  a 4 GB Pi, and it needs a public callback URL or more tailnet plumbing.
- **`tsnet` (embed a Tailscale node in the binary) and call `WhoIs` on each connection.**
  Strongest: identity comes from the connection itself, not a header, so a local process
  can't forge it. But it puts a second Tailscale node and its state on the Pi, and makes
  local development and testing harder. **This is what I'd switch to** if the app ever
  shared a host or network with things I don't trust.
- **Mutual TLS with client certificates.** Strong, but issuing and rotating certificates
  for phones is painful, and Tailscale already gives each device a key.
