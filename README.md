# caddy-anubis

A native Caddy module for [Anubis](https://github.com/TecharoHQ/anubis), exploring
what a "proper" integration looks like beyond the existing prototypes.

This is a work-in-progress integration spike. One middleware directive,
one `libanubis.Server` per block, hardcoded options. The full module
(shared `caddy.App` for cross-site signing keys + store backends, per-route
policy overrides, metrics, hot-reload) is not yet implemented.

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

- **libanubis is unbuildable as a Go module dependency.** Embedded assets
  (`lib/challenge/preact/static/app.js`, `web/static/*`, `xess.min.css`)
  are gitignored and missing from tagged release tarballs. Workaround:
  local clone + `go.work`. Upstream fix would be to commit prebuilt
  assets at release time.
- **libanubis policy parsing uses its own `slog` logger** writing to
  stderr, bypassing whatever logger we pass via `Options.Logger`. Caddy's
  log pipeline doesn't see policy-parse warnings (e.g. Thoth warnings).
  Server runtime logs do flow through correctly via our zapslog adapter.
- libanubis mutates package-level globals (`anubis.BasePrefix`,
  `anubis.PublicUrl`) on every `lib.New()` call. Multi-site usage with
  different prefixes will step on its own toes; the eventual `caddy.App`
  pattern needs to enforce a single global instance.
- No `Cleanup` — store backends (when configured) leak on Caddy reload.
- No replacer support: `policy_file` and friends don't expand `{env.X}` etc.
- No metrics integration with Caddy's Prometheus surface (Anubis registers
  to the global registry, so they show up alongside Caddy's by default).
