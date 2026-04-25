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

One-time setup (paths are arbitrary; pick whatever works for you):

1. Clone Anubis somewhere and build its assets:

   ```sh
   git clone https://github.com/TecharoHQ/anubis
   cd anubis && make assets   # needs npm; on macOS, also `brew install bash`
   ```

2. From this repo, create a `go.work` pointing at that clone (gitignored):

   ```sh
   cat > go.work <<EOF
   go 1.25
   use .
   replace github.com/TecharoHQ/anubis => $(realpath ../anubis)
   EOF
   ```

   Adjust the path to wherever you cloned Anubis.

Then:

```sh
go run ./cmd/caddy run --config Caddyfile
```

Then in a browser, hit `http://localhost:8080`. Expected:
1. First request: Anubis serves a proof-of-work challenge page.
2. Browser solves the PoW, posts the solution, gets an auth cookie.
3. Subsequent requests pass through to `respond "Hello, you passed!"`.

Anubis's own metrics are exposed alongside Caddy's at `/metrics` if you
add an admin/metrics endpoint.

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
- No `Validate` — bad config surfaces only at request time.
- No replacer support: `policy_file` and friends don't expand `{env.X}` etc.
- No metrics integration with Caddy's Prometheus surface (Anubis registers
  to the global registry, so they show up alongside Caddy's by default).
