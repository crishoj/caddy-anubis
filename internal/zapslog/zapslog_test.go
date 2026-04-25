package zapslog

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func newObserver(t *testing.T, level zapcore.Level) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()
	core, recorded := observer.New(level)
	return zap.New(core), recorded
}

func TestHandle_BasicRoundTrip(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl))

	logger.Info("hello", "key", "value", "n", 42)

	logs := recorded.All()
	if len(logs) != 1 {
		t.Fatalf("entries: got %d, want 1", len(logs))
	}
	entry := logs[0]
	if entry.Level != zapcore.InfoLevel {
		t.Errorf("level: got %v, want Info", entry.Level)
	}
	if entry.Message != "hello" {
		t.Errorf("message: got %q, want hello", entry.Message)
	}
	got := entry.ContextMap()
	if got["key"] != "value" {
		t.Errorf("key: got %v, want value", got["key"])
	}
	if got["n"] != int64(42) {
		t.Errorf("n: got %v, want 42", got["n"])
	}
}

func TestEnabled_LevelFiltering(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.WarnLevel)
	logger := slog.New(NewHandler(zl))

	logger.Debug("d")
	logger.Info("i")
	logger.Warn("w")
	logger.Error("e")

	got := recorded.AllUntimed()
	if len(got) != 2 {
		t.Fatalf("entries: got %d, want 2 (warn+error): %+v", len(got), got)
	}
	if got[0].Message != "w" || got[1].Message != "e" {
		t.Errorf("entries: %+v", got)
	}
}

func TestHandle_TimePreserved(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	h := NewHandler(zl)

	want := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	rec := slog.NewRecord(want, slog.LevelInfo, "x", 0)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatal(err)
	}

	logs := recorded.All()
	if len(logs) != 1 {
		t.Fatalf("entries: got %d, want 1", len(logs))
	}
	if !logs[0].Time.Equal(want) {
		t.Errorf("time: got %v, want %v", logs[0].Time, want)
	}
}

func TestWithAttrs_Prepends(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl)).With("base", "ctx")

	logger.Info("m", "leaf", 1)

	got := recorded.All()[0].ContextMap()
	if got["base"] != "ctx" {
		t.Errorf("base: got %v, want ctx", got["base"])
	}
	if got["leaf"] != int64(1) {
		t.Errorf("leaf: got %v, want 1", got["leaf"])
	}
}

func TestWithGroup_Nests(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl)).WithGroup("svc").With("name", "anubis")

	logger.Info("started", "port", 8080)

	got := recorded.All()[0].ContextMap()
	if got["svc.name"] != "anubis" {
		t.Errorf("svc.name: got %v", got)
	}
	if got["svc.port"] != int64(8080) {
		t.Errorf("svc.port: got %v", got)
	}
}

func TestWithGroup_Nested(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl)).WithGroup("a").WithGroup("b").With("k", "v")

	logger.Info("m", "leaf", 1)

	got := recorded.All()[0].ContextMap()
	if got["a.b.k"] != "v" {
		t.Errorf("a.b.k: got %v", got)
	}
	if got["a.b.leaf"] != int64(1) {
		t.Errorf("a.b.leaf: got %v", got)
	}
}

func TestGroupAttr_Flattened(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl))

	logger.Info("m", slog.Group("req", slog.String("path", "/x"), slog.Int("status", 200)))

	got := recorded.All()[0].ContextMap()
	if got["req.path"] != "/x" {
		t.Errorf("req.path: got %v", got)
	}
	if got["req.status"] != int64(200) {
		t.Errorf("req.status: got %v", got)
	}
}

func TestGroupAttr_InlineEmptyKey(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl))

	logger.Info("m", slog.Group("", slog.String("a", "1"), slog.String("b", "2")))

	got := recorded.All()[0].ContextMap()
	if got["a"] != "1" || got["b"] != "2" {
		t.Errorf("inline group: got %v", got)
	}
}

func TestEmptyAttr_Dropped(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl))

	logger.Info("m", slog.String("", "ignored"), slog.String("kept", "yes"))

	got := recorded.All()[0].ContextMap()
	if _, ok := got[""]; ok {
		t.Errorf("empty-key attr was kept: %v", got)
	}
	if got["kept"] != "yes" {
		t.Errorf("kept: got %v", got)
	}
}

func TestLogValuer_Resolved(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl))

	logger.Info("m", "v", logVal{"resolved"})

	got := recorded.All()[0].ContextMap()
	if got["v"] != "resolved" {
		t.Errorf("logvaluer: got %v", got)
	}
}

type logVal struct{ s string }

func (l logVal) LogValue() slog.Value { return slog.StringValue(l.s) }

func TestErrorAttr_Captured(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	logger := slog.New(NewHandler(zl))

	logger.Info("m", "err", errors.New("boom"))

	got := recorded.All()[0].ContextMap()
	if _, ok := got["err"]; !ok {
		t.Errorf("err attr missing: %v", got)
	}
}

func TestWithGroup_AttrsBeforeGroupNotPrefixed(t *testing.T) {
	zl, recorded := newObserver(t, zapcore.DebugLevel)
	// Attrs added *before* WithGroup should not be prefixed by it.
	logger := slog.New(NewHandler(zl)).With("base", "yes").WithGroup("g").With("inner", 1)

	logger.Info("m")

	got := recorded.All()[0].ContextMap()
	if got["base"] != "yes" {
		t.Errorf("base (pre-group): got %v, want yes", got["base"])
	}
	if got["g.inner"] != int64(1) {
		t.Errorf("g.inner: got %v", got["g.inner"])
	}
}
