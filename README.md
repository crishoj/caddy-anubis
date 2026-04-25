# caddy-anubis

[![CI](https://github.com/crishoj/caddy-anubis/actions/workflows/ci.yml/badge.svg)](https://github.com/crishoj/caddy-anubis/actions/workflows/ci.yml)

A native Caddy module for [Anubis](https://github.com/TecharoHQ/anubis).

> **Status: exploratory.** This is a research spike — usable for single-site
> deployments today, but not a stable release and not recommended for
> production without reading the [Known gaps](#known-gaps) section. An
> official native plugin is under discussion in
> [TecharoHQ/anubis#16](https://github.com/TecharoHQ/anubis/issues/16) and
> [PR #1577](https://github.com/TecharoHQ/anubis/pull/1577); if/when that
> lands, this repo is mostly archeology — but the design choices and
> upstream-issue findings here may still be useful reference material.

This is a single-block integration spike: one middleware directive, one
`libanubis.Server` per block. The full module (shared `caddy.App` for
cross-site signing keys + store backends, per-route policy overrides,
metrics, hot-reload) is not yet implemented and is gated on upstream
[issue #1588](https://github.com/TecharoHQ/anubis/issues/1588) (libanubis
package globals).

## Run the spike

libanubis embeds generated JS/CSS assets that are gitignored and not in
the published v1.25.0 tarball, so building against `go get` fails. Until
upstream commits prebuilt assets at release time, you need a local clone
of Anubis with `make assets` run, plus a local `go.work` file pointing
the workspace at it (gitignored — developer-specific).

One-time setup (requires Go 1.25+, npm, Bash 4+):

```sh
make setup     # clones Anubis to ../anubis, builds its assets, writes go.work
make run       # starts Caddy on :8080
```

Override the clone location with `make setup ANUBIS_DIR=path/of/your/choice`.

Then in a browser, hit `http://localhost:8080`. Expected:
1. First request: Anubis serves a proof-of-work challenge page.
2. Browser solves the PoW, posts the solution, gets an auth cookie.
3. Subsequent requests pass through to `respond "Hello, you passed!"`.

Anubis's own metrics are exposed alongside Caddy's at `/metrics` if you
add an admin/metrics endpoint.

## Caddyfile options

```caddyfile
anubis [<policy_file>] {
    policy_file              <path>     # bot policy YAML; built-in default if omitted
    difficulty               <int>      # PoW leading-zero bits; default 4
    ed25519_private_key_hex  <hex>      # 64 hex chars (32-byte seed); see below
    ed25519_private_key_file <path>     # mutually exclusive with the hex form
    redirect_domains         <d>...     # allowlist for ?redir= on PoW pass; globs OK
    serve_robots_txt                    # serve a built-in disallow-all robots.txt
    cookie_expiration        <duration> # cookie + JWT lifetime; default 168h (7d)
}
```

**ED25519 key persistence.** Without `ed25519_private_key_hex` or
`ed25519_private_key_file`, libanubis generates a fresh signing key on every
process start, invalidating all previously issued cookies on each Caddy
reload. For deployments with returning visitors, set a stable key:

```sh
openssl rand -hex 32 > /etc/anubis/key.hex
```

Then in your Caddyfile:

```caddyfile
anubis {
    ed25519_private_key_file /etc/anubis/key.hex
}
```

**`redirect_domains`** is a security boundary. The `?redir=` parameter on
the PoW pass URL is otherwise open-redirect to any host. Set this to your
own domain(s) in production.

## Pieces

- `anubis.go` — the `http.handlers.anubis` Caddy module.
- `cmd/caddy/main.go` — minimal Caddy build with this module + standard
  Caddy modules. Lets you `go run ./cmd/caddy` without xcaddy.
- `Caddyfile` — example config exercising the middleware.
- `internal/nexterr` — context-state pattern that captures errors returned by
  Caddy's `next` handler when invoked from inside `libanubis.Server`. Lets
  upstream errors propagate back to Caddy's `handle_errors` chain instead of
  being swallowed by `http.Handler`'s no-error contract.

For the Caddy `*zap.Logger` → Anubis `*slog.Logger` bridge we use upstream
[`go.uber.org/zap/exp/zapslog`](https://pkg.go.dev/go.uber.org/zap/exp/zapslog)
rather than rolling our own — it produces properly structured zap groups
and respects the slog empty-group spec edge case.

## Known gaps

Items marked _upstream_ depend on changes in libanubis itself.

- **libanubis is unbuildable as a Go module dependency.** Embedded assets
  (`lib/challenge/preact/static/app.js`, `web/static/*`, `xess.min.css`)
  are gitignored and missing from tagged release tarballs. Workaround:
  local clone + `go.work` (see setup above). _upstream:
  [#1587](https://github.com/TecharoHQ/anubis/issues/1587)._
- **libanubis policy parsing uses its own `slog` logger** writing to
  stderr, bypassing whatever logger we pass via `Options.Logger`. Caddy's
  log pipeline doesn't see policy-parse warnings (e.g. Thoth warnings).
  Server runtime logs do flow through correctly via our zapslog adapter.
  _upstream: [#1589](https://github.com/TecharoHQ/anubis/issues/1589)._
- **libanubis mutates package-level globals** (`anubis.BasePrefix`,
  `anubis.PublicUrl`) on every `lib.New()` call. Multi-site usage with
  different prefixes silently corrupts cookies; the eventual `caddy.App`
  pattern needs this fixed first. _upstream:
  [#1588](https://github.com/TecharoHQ/anubis/issues/1588)._
- No `Cleanup` — store backends (when configured) leak on Caddy reload.
- No replacer support: `policy_file` and friends don't expand `{env.X}` etc.
- No metrics integration with Caddy's Prometheus surface (Anubis registers
  to the global registry, so they show up alongside Caddy's by default).

## Upstream contributions

While building this, six findings were raised on
[TecharoHQ/anubis](https://github.com/TecharoHQ/anubis):

- PR [#1585](https://github.com/TecharoHQ/anubis/pull/1585) — Makefile
  `$(CURDIR)` fix so `make -C` works.
- PR [#1586](https://github.com/TecharoHQ/anubis/pull/1586) —
  `Options.CookieExpiration` zero-value default
  (`anubis.CookieDefaultExpirationTime`), avoiding expired-on-arrival
  cookies and the resulting infinite challenge loop for library consumers.
- Issue [#1587](https://github.com/TecharoHQ/anubis/issues/1587) —
  embedded assets gitignored, breaking go-module consumption.
- Issue [#1588](https://github.com/TecharoHQ/anubis/issues/1588) —
  `anubis.BasePrefix`/`anubis.PublicUrl` are package globals.
- Issue [#1589](https://github.com/TecharoHQ/anubis/issues/1589) —
  `policy.ParseConfig` ignores `Options.Logger`.
- Comment on [PR #1577](https://github.com/TecharoHQ/anubis/pull/1577) —
  Caddy directive ordering (`Before "reverse_proxy"` vs.
  `After "templates"`).
