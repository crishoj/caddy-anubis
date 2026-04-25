package caddyanubis

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// validKeyHex is a 32-byte ED25519 seed encoded as 64 hex chars. Used in
// table tests that need a valid hex key without caring about the bytes.
const validKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestUnmarshalCaddyfile(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		check   func(t *testing.T, m *Middleware)
	}{
		{
			name:  "no args, no block",
			input: `anubis`,
		},
		{
			name:  "inline policy file",
			input: `anubis /etc/anubis/policy.yaml`,
			check: func(t *testing.T, m *Middleware) {
				if m.PolicyFile != "/etc/anubis/policy.yaml" {
					t.Errorf("PolicyFile = %q", m.PolicyFile)
				}
			},
		},
		{
			name: "block: policy_file",
			input: `anubis {
				policy_file /etc/anubis/p.yaml
			}`,
			check: func(t *testing.T, m *Middleware) {
				if m.PolicyFile != "/etc/anubis/p.yaml" {
					t.Errorf("PolicyFile = %q", m.PolicyFile)
				}
			},
		},
		{
			name: "block: difficulty",
			input: `anubis {
				difficulty 6
			}`,
			check: func(t *testing.T, m *Middleware) {
				if m.Difficulty != 6 {
					t.Errorf("Difficulty = %d", m.Difficulty)
				}
			},
		},
		{
			name: "block: difficulty not a number",
			input: `anubis {
				difficulty notanumber
			}`,
			wantErr: true,
		},
		{
			name: "block: difficulty missing arg",
			input: `anubis {
				difficulty
			}`,
			wantErr: true,
		},
		{
			name: "block: ed25519_private_key_hex",
			input: `anubis {
				ed25519_private_key_hex deadbeef
			}`,
			check: func(t *testing.T, m *Middleware) {
				if m.ED25519PrivateKeyHex != "deadbeef" {
					t.Errorf("ED25519PrivateKeyHex = %q", m.ED25519PrivateKeyHex)
				}
			},
		},
		{
			name: "block: ed25519_private_key_file",
			input: `anubis {
				ed25519_private_key_file /etc/anubis/key.hex
			}`,
			check: func(t *testing.T, m *Middleware) {
				if m.ED25519PrivateKeyFile != "/etc/anubis/key.hex" {
					t.Errorf("ED25519PrivateKeyFile = %q", m.ED25519PrivateKeyFile)
				}
			},
		},
		{
			name: "block: redirect_domains single",
			input: `anubis {
				redirect_domains example.com
			}`,
			check: func(t *testing.T, m *Middleware) {
				want := []string{"example.com"}
				if !reflect.DeepEqual(m.RedirectDomains, want) {
					t.Errorf("RedirectDomains = %v, want %v", m.RedirectDomains, want)
				}
			},
		},
		{
			name: "block: redirect_domains multiple with glob",
			input: `anubis {
				redirect_domains example.com *.example.com other.test
			}`,
			check: func(t *testing.T, m *Middleware) {
				want := []string{"example.com", "*.example.com", "other.test"}
				if !reflect.DeepEqual(m.RedirectDomains, want) {
					t.Errorf("RedirectDomains = %v, want %v", m.RedirectDomains, want)
				}
			},
		},
		{
			name: "block: redirect_domains requires args",
			input: `anubis {
				redirect_domains
			}`,
			wantErr: true,
		},
		{
			name: "block: serve_robots_txt as flag",
			input: `anubis {
				serve_robots_txt
			}`,
			check: func(t *testing.T, m *Middleware) {
				if !m.ServeRobotsTXT {
					t.Error("ServeRobotsTXT = false, want true")
				}
			},
		},
		{
			name: "block: serve_robots_txt rejects arg",
			input: `anubis {
				serve_robots_txt yes
			}`,
			wantErr: true,
		},
		{
			name: "block: cookie_expiration",
			input: `anubis {
				cookie_expiration 24h
			}`,
			check: func(t *testing.T, m *Middleware) {
				if got := time.Duration(m.CookieExpiration); got != 24*time.Hour {
					t.Errorf("CookieExpiration = %v, want 24h", got)
				}
			},
		},
		{
			name: "block: cookie_expiration invalid",
			input: `anubis {
				cookie_expiration not-a-duration
			}`,
			wantErr: true,
		},
		{
			name: "block: unknown subdirective",
			input: `anubis {
				no_such_thing yes
			}`,
			wantErr: true,
		},
		{
			name: "block: combined options",
			input: `anubis /etc/anubis/policy.yaml {
				difficulty 5
				ed25519_private_key_file /etc/anubis/key.hex
				redirect_domains example.com *.example.com
				serve_robots_txt
				cookie_expiration 168h
			}`,
			check: func(t *testing.T, m *Middleware) {
				if m.PolicyFile != "/etc/anubis/policy.yaml" {
					t.Errorf("PolicyFile = %q", m.PolicyFile)
				}
				if m.Difficulty != 5 {
					t.Errorf("Difficulty = %d", m.Difficulty)
				}
				if m.ED25519PrivateKeyFile != "/etc/anubis/key.hex" {
					t.Errorf("ED25519PrivateKeyFile = %q", m.ED25519PrivateKeyFile)
				}
				if !reflect.DeepEqual(m.RedirectDomains, []string{"example.com", "*.example.com"}) {
					t.Errorf("RedirectDomains = %v", m.RedirectDomains)
				}
				if !m.ServeRobotsTXT {
					t.Error("ServeRobotsTXT = false")
				}
				if got := time.Duration(m.CookieExpiration); got != 168*time.Hour {
					t.Errorf("CookieExpiration = %v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Middleware
			d := caddyfile.NewTestDispenser(tt.input)
			err := m.UnmarshalCaddyfile(d)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalCaddyfile error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, &m)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	// Set up a real existing file for the policy_file positive case.
	tmp := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(tmp, []byte("bots: []\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		m         Middleware
		wantErr   bool
		errSubstr string
	}{
		{
			name: "empty is valid",
		},
		{
			name: "valid hex key",
			m:    Middleware{ED25519PrivateKeyHex: validKeyHex},
		},
		{
			name: "valid file path (file exists)",
			m:    Middleware{ED25519PrivateKeyFile: "/some/path/that/does/not/need/to/exist"},
			// Note: Validate doesn't stat the key file (file may be created
			// later by an external process). We only check it's not also-set
			// alongside the hex form.
		},
		{
			name:      "both hex and file set",
			m:         Middleware{ED25519PrivateKeyHex: validKeyHex, ED25519PrivateKeyFile: "/foo"},
			wantErr:   true,
			errSubstr: "mutually exclusive",
		},
		{
			name:      "hex too short",
			m:         Middleware{ED25519PrivateKeyHex: "deadbeef"},
			wantErr:   true,
			errSubstr: "expected 64 hex chars",
		},
		{
			name:      "hex too long",
			m:         Middleware{ED25519PrivateKeyHex: validKeyHex + "aa"},
			wantErr:   true,
			errSubstr: "expected 64 hex chars",
		},
		{
			name:      "hex invalid chars",
			m:         Middleware{ED25519PrivateKeyHex: strings.Repeat("z", 64)},
			wantErr:   true,
			errSubstr: "invalid hex",
		},
		{
			name: "policy_file exists",
			m:    Middleware{PolicyFile: tmp},
		},
		{
			name:      "policy_file does not exist",
			m:         Middleware{PolicyFile: "/no/such/file/qwertyuiop.yaml"},
			wantErr:   true,
			errSubstr: "policy_file",
		},
		{
			name:      "difficulty too high",
			m:         Middleware{Difficulty: 100},
			wantErr:   true,
			errSubstr: "difficulty",
		},
		{
			name:      "difficulty negative",
			m:         Middleware{Difficulty: -1},
			wantErr:   true,
			errSubstr: "difficulty",
		},
		{
			name: "difficulty zero (use default)",
			m:    Middleware{Difficulty: 0},
		},
		{
			name: "difficulty at upper bound",
			m:    Middleware{Difficulty: 32},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil && tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.errSubstr)
			}
		})
	}
}

func TestDecodeED25519Hex(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid", validKeyHex, false},
		{"valid with surrounding whitespace", "\n  " + validKeyHex + "  \n", false},
		{"too short", "deadbeef", true},
		{"too long", validKeyHex + "aa", true},
		{"invalid hex chars", strings.Repeat("z", 64), true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := decodeED25519Hex(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil && len(key) == 0 {
				t.Error("returned empty key on success")
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		caddyVar   any // value to write under caddyhttp.ClientIPVarKey; nil = don't set
		want       string
	}{
		{
			name:       "no caddy var, ipv4 with port",
			remoteAddr: "192.0.2.1:9999",
			want:       "192.0.2.1",
		},
		{
			name:       "no caddy var, ipv6 with port",
			remoteAddr: "[2001:db8::1]:9999",
			want:       "2001:db8::1",
		},
		{
			name:       "no caddy var, ipv4 without port",
			remoteAddr: "192.0.2.1",
			want:       "192.0.2.1",
		},
		{
			name:       "caddy var preferred over RemoteAddr (with port)",
			remoteAddr: "192.0.2.1:9999",
			caddyVar:   "10.0.0.1:1234",
			want:       "10.0.0.1",
		},
		{
			name:       "caddy var preferred over RemoteAddr (without port)",
			remoteAddr: "192.0.2.1:9999",
			caddyVar:   "10.0.0.1",
			want:       "10.0.0.1",
		},
		{
			name:       "caddy var empty string falls back to RemoteAddr",
			remoteAddr: "192.0.2.1:9999",
			caddyVar:   "",
			want:       "192.0.2.1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.caddyVar != nil {
				vars := map[string]any{caddyhttp.ClientIPVarKey: tt.caddyVar}
				ctx := context.WithValue(r.Context(), caddyhttp.VarsCtxKey, vars)
				r = r.WithContext(ctx)
			}
			if got := clientIP(r); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Ensure the Middleware struct satisfies the interfaces it claims. This is
// already enforced by the var-block at the bottom of anubis.go, but explicit
// is better and gives a useful failure message.
func TestInterfaces(t *testing.T) {
	var _ caddy.Provisioner = (*Middleware)(nil)
	var _ caddy.Validator = (*Middleware)(nil)
	var _ caddyfile.Unmarshaler = (*Middleware)(nil)
}
