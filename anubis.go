// Package caddyanubis provides an Anubis HTTP middleware module for Caddy.
//
// Build standalone with `go run ./cmd/caddy` against the example Caddyfile,
// or via xcaddy:
//
//	xcaddy build --with github.com/crishoj/caddy-anubis
//
// This package is currently a thin integration spike: a single middleware
// directive with one libanubis.Server per block. The full module (shared
// caddy.App for cross-site signing key + store, per-route policy overrides)
// is not yet implemented.
package caddyanubis

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/TecharoHQ/anubis"
	libanubis "github.com/TecharoHQ/anubis/lib"

	// Memory store backend; required for the default policy. Other backends
	// (bbolt, valkey, s3) can be opted into by additional blank imports.
	_ "github.com/TecharoHQ/anubis/lib/store/memory"

	"github.com/crishoj/caddy-anubis/internal/nexterr"
)

func init() {
	caddy.RegisterModule(Middleware{})
	httpcaddyfile.RegisterHandlerDirective("anubis", parseCaddyfile)
	// Place anubis after the last middleware directive (templates) so it
	// runs before any response-generating handler — respond, reverse_proxy,
	// file_server, abort, etc. PR #1577 in TecharoHQ/anubis used `Before
	// "reverse_proxy"` which is index 94, but `respond` is at index 92, so
	// `respond` would run first and short-circuit anubis.
	httpcaddyfile.RegisterDirectiveOrder("anubis", httpcaddyfile.After, "templates")
}

// Middleware wraps requests with the Anubis bot challenge, delegating to the
// next Caddy handler when the request passes (cookie present and valid, or
// policy doesn't require a challenge).
type Middleware struct {
	// Difficulty overrides the proof-of-work difficulty (number of leading
	// zero bits). Defaults to 4 when zero.
	Difficulty int `json:"difficulty,omitempty"`

	// PolicyFile is the path to an Anubis bot-policy YAML file. When empty,
	// Anubis's built-in default policy is used (memory store, default rules).
	PolicyFile string `json:"policy_file,omitempty"`

	server *libanubis.Server
	logger *zap.Logger
}

// CaddyModule returns the Caddy module information.
func (Middleware) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.anubis",
		New: func() caddy.Module { return new(Middleware) },
	}
}

// Provision loads the policy and constructs the Anubis server.
func (m *Middleware) Provision(ctx caddy.Context) error {
	m.logger = ctx.Logger()

	difficulty := m.Difficulty
	if difficulty == 0 {
		difficulty = 4
	}

	policy, err := libanubis.LoadPoliciesOrDefault(ctx, m.PolicyFile, difficulty, "INFO")
	if err != nil {
		return fmt.Errorf("anubis: load policy: %w", err)
	}

	server, err := libanubis.New(libanubis.Options{
		Next:   nexterr.Forwarder{},
		Policy: policy,
		// libanubis bakes CookieExpiration into both the cookie's Expires
		// attribute and the JWT's exp claim. Leaving it at the zero value
		// produces expired-on-arrival cookies and an infinite challenge loop.
		CookieExpiration: anubis.CookieDefaultExpirationTime,
		Logger: slog.New(zapslog.NewHandler(
			m.logger.Core(),
			zapslog.WithName("anubis"),
		)),
	})
	if err != nil {
		return fmt.Errorf("anubis: new server: %w", err)
	}
	m.server = server
	return nil
}

// ServeHTTP injects the Caddy next handler into the request context, then
// delegates to the Anubis server. Any error returned by the next handler
// (when invoked through nexterr.Forwarder) is propagated back to Caddy.
//
// libanubis requires X-Real-Ip to be set on every request for IP-based
// policy. We populate it from Caddy's resolved client IP, which respects
// any configured trusted_proxies. This means Caddy is the source of truth
// for what the client IP is — exactly what users expect when they've
// configured trusted_proxies once at the Caddy level.
func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	r = r.Clone(r.Context())
	r.Header.Set("X-Real-Ip", clientIP(r))

	ctx, state := nexterr.WithNext(r.Context(), next)
	m.server.ServeHTTP(w, r.WithContext(ctx))
	return state.Err()
}

// clientIP returns the resolved client IP for r, using Caddy's trusted_proxies
// resolution when available and falling back to the TCP remote address.
func clientIP(r *http.Request) string {
	if v := caddyhttp.GetVar(r.Context(), caddyhttp.ClientIPVarKey); v != nil {
		if addr, ok := v.(string); ok && addr != "" {
			if host, _, err := net.SplitHostPort(addr); err == nil {
				return host
			}
			return addr
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// UnmarshalCaddyfile parses tokens of the form:
//
//	anubis [<policy_file>] {
//	    policy_file <path>
//	    difficulty  <int>
//	}
func (m *Middleware) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()
	if d.NextArg() {
		m.PolicyFile = d.Val()
	}
	for d.NextBlock(0) {
		switch d.Val() {
		case "policy_file":
			if !d.NextArg() {
				return d.ArgErr()
			}
			m.PolicyFile = d.Val()
		case "difficulty":
			if !d.NextArg() {
				return d.ArgErr()
			}
			var n int
			if _, err := fmt.Sscanf(d.Val(), "%d", &n); err != nil {
				return d.Errf("invalid difficulty %q: %v", d.Val(), err)
			}
			m.Difficulty = n
		default:
			return d.Errf("unknown anubis subdirective %q", d.Val())
		}
	}
	return nil
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m Middleware
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return &m, err
}

var (
	_ caddy.Provisioner           = (*Middleware)(nil)
	_ caddyhttp.MiddlewareHandler = (*Middleware)(nil)
	_ caddyfile.Unmarshaler       = (*Middleware)(nil)
)
