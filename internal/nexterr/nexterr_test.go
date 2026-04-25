package nexterr

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// fakeAnubis simulates lib.Server: constructed once with a Next http.Handler
// and dispatches each request to one of two paths based on a header. Anubis
// itself decides whether to challenge or forward; this fake mirrors the
// shape (own response vs. delegate to Next) without the real challenge code.
type fakeAnubis struct {
	next http.Handler
}

func (f *fakeAnubis) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("challenge") == "1" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "challenge")
		return
	}
	f.next.ServeHTTP(w, r)
}

func TestForwarder_NextCalled(t *testing.T) {
	fake := &fakeAnubis{next: Forwarder{}}

	caddyNext := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "upstream")
		return nil
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ctx, state := WithNext(req.Context(), caddyNext)

	fake.ServeHTTP(w, req.WithContext(ctx))

	if !state.Called() {
		t.Error("Called() = false; want true")
	}
	if state.Err() != nil {
		t.Errorf("Err() = %v; want nil", state.Err())
	}
	if got := w.Body.String(); got != "upstream" {
		t.Errorf("body = %q; want upstream", got)
	}
}

func TestForwarder_NextSkipped(t *testing.T) {
	fake := &fakeAnubis{next: Forwarder{}}

	caddyNext := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		t.Fatal("next should not have been called")
		return nil
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("challenge", "1")
	w := httptest.NewRecorder()
	ctx, state := WithNext(req.Context(), caddyNext)

	fake.ServeHTTP(w, req.WithContext(ctx))

	if state.Called() {
		t.Error("Called() = true; want false")
	}
	if state.Err() != nil {
		t.Errorf("Err() = %v; want nil", state.Err())
	}
	if got := w.Body.String(); got != "challenge" {
		t.Errorf("body = %q; want challenge", got)
	}
}

func TestForwarder_NextErrorPropagated(t *testing.T) {
	fake := &fakeAnubis{next: Forwarder{}}

	wantErr := errors.New("upstream broke")
	caddyNext := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return wantErr
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ctx, state := WithNext(req.Context(), caddyNext)

	fake.ServeHTTP(w, req.WithContext(ctx))

	if !state.Called() {
		t.Error("Called() = false; want true")
	}
	if !errors.Is(state.Err(), wantErr) {
		t.Errorf("Err() = %v; want %v", state.Err(), wantErr)
	}
}

func TestForwarder_NoStateInContext(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	Forwarder{}.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", w.Code)
	}
}

// IndependentRequests verifies that two concurrent requests through the same
// fakeAnubis instance get independent State values — i.e. State is per-request,
// not per-process.
func TestForwarder_IndependentRequestState(t *testing.T) {
	fake := &fakeAnubis{next: Forwarder{}}

	hitA := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return errors.New("A failed")
	})
	hitB := caddyhttp.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return nil
	})

	reqA := httptest.NewRequest("GET", "/a", nil)
	reqB := httptest.NewRequest("GET", "/b", nil)
	wA := httptest.NewRecorder()
	wB := httptest.NewRecorder()

	ctxA, stateA := WithNext(reqA.Context(), hitA)
	ctxB, stateB := WithNext(reqB.Context(), hitB)

	fake.ServeHTTP(wA, reqA.WithContext(ctxA))
	fake.ServeHTTP(wB, reqB.WithContext(ctxB))

	if stateA.Err() == nil {
		t.Error("stateA.Err() = nil; want non-nil")
	}
	if stateB.Err() != nil {
		t.Errorf("stateB.Err() = %v; want nil", stateB.Err())
	}
}
