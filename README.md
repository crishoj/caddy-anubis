# caddy-anubis

A native Caddy module for [Anubis](https://github.com/TecharoHQ/anubis), exploring
what a "proper" integration looks like beyond the existing prototypes.

This is a work-in-progress. The repo currently holds one proof-of-concept
package validating a non-trivial part of the integration design:

- `internal/nexterr` — context-state pattern that captures errors returned by
  Caddy's `next` handler when invoked from inside `libanubis.Server`. Lets
  upstream errors propagate back to Caddy's `handle_errors` chain instead of
  being swallowed by `http.Handler`'s no-error contract.

For the Caddy `*zap.Logger` → Anubis `*slog.Logger` bridge we use upstream
[`go.uber.org/zap/exp/zapslog`](https://pkg.go.dev/go.uber.org/zap/exp/zapslog).
We initially wrote our own; comparison showed upstream produces properly
structured zap groups, respects the slog empty-group spec edge case, and
brings caller/stacktrace options for free.

Once the PoC composes cleanly with the real `libanubis`, the plan is to expand
into a full module with a shared `caddy.App` for cross-site signing key +
store backend, and per-route middleware blocks for site-specific
policy/difficulty.
