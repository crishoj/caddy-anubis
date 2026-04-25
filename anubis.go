// Package caddyanubis provides an Anubis HTTP middleware module for Caddy.
//
// Build standalone with `go run ./cmd/caddy` against the example Caddyfile,
// or via xcaddy:
//
//	xcaddy build --with github.com/crishoj/caddy-anubis
//
// This package is currently a single-block integration spike. The full
// module (shared caddy.App for cross-site signing key + store, per-route
// policy overrides) is not yet implemented; see README for known gaps.
package caddyanubis

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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

	// ED25519PrivateKeyHex is a hex-encoded 32-byte ED25519 seed used to sign
	// challenge JWTs. When empty (and ED25519PrivateKeyFile is also empty),
	// libanubis generates a fresh key per process start, which invalidates
	// every previously issued cookie on each Caddy reload. For deployments
	// with returning visitors, set this to a stable value.
	//
	// Generate with: `openssl rand -hex 32`.
	ED25519PrivateKeyHex string `json:"ed25519_private_key_hex,omitempty"`

	// ED25519PrivateKeyFile is the path to a file containing a hex-encoded
	// 32-byte ED25519 seed. Mutually exclusive with ED25519PrivateKeyHex;
	// useful for secrets stored in Kubernetes/Vault/etc.
	ED25519PrivateKeyFile string `json:"ed25519_private_key_file,omitempty"`

	// RedirectDomains is the allowlist of domains Anubis is permitted to
	// redirect to after a successful challenge. Globs (`*`) supported. When
	// empty, any domain is allowed — open-redirect risk via the `?redir=`
	// parameter on PoW pass.
	RedirectDomains []string `json:"redirect_domains,omitempty"`

	// ServeRobotsTXT, when true, makes Anubis serve a built-in robots.txt
	// disallowing all crawlers at /robots.txt and /.well-known/robots.txt.
	ServeRobotsTXT bool `json:"serve_robots_txt,omitempty"`

	// CookieExpiration overrides the default cookie + JWT lifetime
	// (anubis.CookieDefaultExpirationTime, 7 days).
	CookieExpiration caddy.Duration `json:"cookie_expiration,omitempty"`

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

// Validate checks the static configuration before Provision constructs the
// Anubis server. Returning an error here surfaces problems at config-load
// time rather than as 500s on the first request.
func (m *Middleware) Validate() error {
	if m.ED25519PrivateKeyHex != "" && m.ED25519PrivateKeyFile != "" {
		return errors.New("ed25519_private_key_hex and ed25519_private_key_file are mutually exclusive")
	}
	if m.ED25519PrivateKeyHex != "" {
		if _, err := decodeED25519Hex(m.ED25519PrivateKeyHex); err != nil {
			return fmt.Errorf("ed25519_private_key_hex: %w", err)
		}
	}
	if m.PolicyFile != "" {
		if _, err := os.Stat(m.PolicyFile); err != nil {
			return fmt.Errorf("policy_file: %w", err)
		}
	}
	if m.Difficulty < 0 || m.Difficulty > 32 {
		return fmt.Errorf("difficulty must be in [0, 32], got %d", m.Difficulty)
	}
	return nil
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

	opts := libanubis.Options{
		Next:   nexterr.Forwarder{},
		Policy: policy,
		// libanubis bakes CookieExpiration into both the cookie's Expires
		// attribute and the JWT's exp claim. Leaving it at the zero value
		// produces expired-on-arrival cookies and an infinite challenge loop.
		CookieExpiration: anubis.CookieDefaultExpirationTime,
		RedirectDomains:  m.RedirectDomains,
		ServeRobotsTXT:   m.ServeRobotsTXT,
		Logger: slog.New(zapslog.NewHandler(
			m.logger.Core(),
			zapslog.WithName("anubis"),
		)),
	}
	if d := time.Duration(m.CookieExpiration); d > 0 {
		opts.CookieExpiration = d
	}
	if m.ED25519PrivateKeyHex != "" || m.ED25519PrivateKeyFile != "" {
		hexStr := m.ED25519PrivateKeyHex
		if hexStr == "" {
			b, err := os.ReadFile(m.ED25519PrivateKeyFile)
			if err != nil {
				return fmt.Errorf("anubis: reading ed25519_private_key_file: %w", err)
			}
			hexStr = string(b)
		}
		key, err := decodeED25519Hex(hexStr)
		if err != nil {
			return fmt.Errorf("anubis: ed25519 key: %w", err)
		}
		opts.ED25519PrivateKey = key
	}

	server, err := libanubis.New(opts)
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

// decodeED25519Hex parses a hex-encoded 32-byte ED25519 seed into a
// PrivateKey. Whitespace around the hex string is trimmed (so reading from
// a file with a trailing newline works).
func decodeED25519Hex(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(s)
	wantLen := hex.EncodedLen(ed25519.SeedSize)
	if len(s) != wantLen {
		return nil, fmt.Errorf("expected %d hex chars (32-byte seed), got %d", wantLen, len(s))
	}
	seed, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// UnmarshalCaddyfile parses tokens of the form:
//
//	anubis [<policy_file>] {
//	    policy_file              <path>
//	    difficulty               <int>
//	    ed25519_private_key_hex  <hex>      # 64 hex chars (32-byte seed)
//	    ed25519_private_key_file <path>     # mutually exclusive with the hex form
//	    redirect_domains         <d>...     # globs allowed
//	    serve_robots_txt
//	    cookie_expiration        <duration> # default 7d
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
			n, err := strconv.Atoi(d.Val())
			if err != nil {
				return d.Errf("invalid difficulty %q: %v", d.Val(), err)
			}
			m.Difficulty = n
		case "ed25519_private_key_hex":
			if !d.NextArg() {
				return d.ArgErr()
			}
			m.ED25519PrivateKeyHex = d.Val()
		case "ed25519_private_key_file":
			if !d.NextArg() {
				return d.ArgErr()
			}
			m.ED25519PrivateKeyFile = d.Val()
		case "redirect_domains":
			m.RedirectDomains = d.RemainingArgs()
			if len(m.RedirectDomains) == 0 {
				return d.ArgErr()
			}
		case "serve_robots_txt":
			if d.NextArg() {
				return d.Errf("serve_robots_txt is a flag, takes no arguments")
			}
			m.ServeRobotsTXT = true
		case "cookie_expiration":
			if !d.NextArg() {
				return d.ArgErr()
			}
			dur, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return d.Errf("invalid cookie_expiration %q: %v", d.Val(), err)
			}
			m.CookieExpiration = caddy.Duration(dur)
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
	_ caddy.Validator             = (*Middleware)(nil)
	_ caddyhttp.MiddlewareHandler = (*Middleware)(nil)
	_ caddyfile.Unmarshaler       = (*Middleware)(nil)
)
