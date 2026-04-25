// Package zapslog provides a slog.Handler that delegates to a zap.Logger.
//
// Caddy modules receive a *zap.Logger from ctx.Logger(); some upstream
// libraries (notably Anubis) consume *slog.Logger instead. NewHandler bridges
// the two so a single configured logger drives both, preserving structured
// fields and respecting the underlying zap.Logger's level configuration.
//
// Group keys are flattened with dot separators ("a.b.k"), matching the slog
// convention used by encoders such as json. Empty group keys inline their
// children at the parent level, also per the slog spec.
package zapslog

import (
	"context"
	"log/slog"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewHandler returns a slog.Handler that delegates to base. Records logged
// through the resulting handler are emitted via base.Core, respecting its
// level configuration and any baked-in fields. The returned handler preserves
// base's logger name.
func NewHandler(base *zap.Logger) slog.Handler {
	return &handler{base: base}
}

type handler struct {
	base   *zap.Logger
	prefix string      // accumulated group prefix; ends in "." when non-empty
	fields []zap.Field // baked-in fields from prior WithAttrs calls
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return h.base.Core().Enabled(toZapLevel(level))
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	ce := h.base.Check(toZapLevel(r.Level), r.Message)
	if ce == nil {
		return nil
	}
	if !r.Time.IsZero() {
		ce.Time = r.Time
	}

	fields := make([]zap.Field, 0, len(h.fields)+r.NumAttrs())
	fields = append(fields, h.fields...)
	r.Attrs(func(a slog.Attr) bool {
		fields = appendField(fields, h.prefix, a)
		return true
	})

	ce.Write(fields...)
	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	nh := *h
	nh.fields = make([]zap.Field, len(h.fields), len(h.fields)+len(attrs))
	copy(nh.fields, h.fields)
	for _, a := range attrs {
		nh.fields = appendField(nh.fields, h.prefix, a)
	}
	return &nh
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := *h
	nh.prefix = h.prefix + name + "."
	return &nh
}

func toZapLevel(l slog.Level) zapcore.Level {
	switch {
	case l < slog.LevelInfo:
		return zapcore.DebugLevel
	case l < slog.LevelWarn:
		return zapcore.InfoLevel
	case l < slog.LevelError:
		return zapcore.WarnLevel
	default:
		return zapcore.ErrorLevel
	}
}

// appendField converts a slog.Attr into one or more zap.Fields, prepending
// prefix to the attr key. Group attrs are flattened recursively; an empty
// group key inlines children at the parent level.
func appendField(out []zap.Field, prefix string, a slog.Attr) []zap.Field {
	a.Value = a.Value.Resolve()

	if a.Value.Kind() == slog.KindGroup {
		gp := prefix
		if a.Key != "" {
			gp = prefix + a.Key + "."
		}
		for _, ga := range a.Value.Group() {
			out = appendField(out, gp, ga)
		}
		return out
	}

	if a.Key == "" {
		// Per slog convention: drop attrs with empty key and non-group value.
		return out
	}

	key := prefix + a.Key
	switch a.Value.Kind() {
	case slog.KindBool:
		return append(out, zap.Bool(key, a.Value.Bool()))
	case slog.KindDuration:
		return append(out, zap.Duration(key, a.Value.Duration()))
	case slog.KindFloat64:
		return append(out, zap.Float64(key, a.Value.Float64()))
	case slog.KindInt64:
		return append(out, zap.Int64(key, a.Value.Int64()))
	case slog.KindString:
		return append(out, zap.String(key, a.Value.String()))
	case slog.KindTime:
		return append(out, zap.Time(key, a.Value.Time()))
	case slog.KindUint64:
		return append(out, zap.Uint64(key, a.Value.Uint64()))
	default:
		return append(out, zap.Any(key, a.Value.Any()))
	}
}
