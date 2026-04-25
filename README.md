# caddy-anubis

A native Caddy module for [Anubis](https://github.com/TecharoHQ/anubis), exploring
what a "proper" integration looks like beyond the existing prototypes.

This is a work-in-progress. Right now the repo contains two proof-of-concept
internal packages validating the integration design:

- `internal/zapslog` — `slog.Handler` that delegates to a `zap.Core`, so Caddy's
  configured logger can drive Anubis's `*slog.Logger`. Existing prototypes
  discard Caddy's logger; this restores unified logging.
- `internal/nexterr` — context-state pattern that captures errors returned by
  Caddy's `next` handler when invoked from inside `libanubis.Server`. Lets
  upstream errors propagate back to Caddy's `handle_errors` chain instead of
  being swallowed by `http.Handler`'s no-error contract.

Once both PoCs prove out, the plan is to expand into a full module with a
shared `caddy.App` for cross-site signing key + store backend, and per-route
middleware blocks for site-specific policy/difficulty.
