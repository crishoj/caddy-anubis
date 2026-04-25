// Package nexterr captures errors returned by Caddy's next handler when it is
// invoked from inside a library that only sees an http.Handler interface.
//
// The Anubis lib.Server is constructed once with an http.Handler as its
// "next" callback and dispatches to it via ServeHTTP, which returns nothing.
// Caddy middleware, by contrast, expects errors from the next handler to
// propagate so handle_errors directives can fire. nexterr bridges this by
// stashing the per-request Caddy next handler in the request context and
// attaching a stateless [Forwarder] as the library's Next. When the library
// invokes Forwarder.ServeHTTP the real Caddy next handler runs and any error
// it returns is captured in the per-request [State].
//
// Concurrency: a State value is intended to be used by exactly one request
// goroutine. The library wrapping it must not invoke its Next callback from
// multiple goroutines concurrently for the same request.
package nexterr

import (
	"context"
	"net/http"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

type stateKey struct{}

// State holds the per-request next handler and what happened when it was
// invoked. Obtain one via [WithNext]; inspect via Called and Err after the
// wrapping library returns.
type State struct {
	next   caddyhttp.Handler
	err    error
	called bool
}

// Called reports whether the next handler was invoked.
func (s *State) Called() bool { return s.called }

// Err returns the error returned by the next handler, or nil if it was not
// called or returned nil.
func (s *State) Err() error { return s.err }

// WithNext attaches next to ctx and returns the resulting context plus a
// State that records what happens when next is invoked through [Forwarder].
func WithNext(ctx context.Context, next caddyhttp.Handler) (context.Context, *State) {
	s := &State{next: next}
	return context.WithValue(ctx, stateKey{}, s), s
}

// Forwarder is a stateless http.Handler that retrieves a [State] from the
// request context and delegates to the captured Caddy next handler. The zero
// value is ready to use; pass Forwarder{} as the wrapping library's Next.
type Forwarder struct{}

// ServeHTTP implements http.Handler. If no State is attached to the request
// context (a contract violation by the caller of [WithNext]), it writes a 500
// and returns; this should never happen in practice.
func (Forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s, ok := r.Context().Value(stateKey{}).(*State)
	if !ok || s == nil {
		http.Error(w, "nexterr: missing state in request context", http.StatusInternalServerError)
		return
	}
	s.called = true
	s.err = s.next.ServeHTTP(w, r)
}
