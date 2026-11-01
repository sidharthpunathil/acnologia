package redact

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
)

// Handler is a slog.Handler that redacts the message and every string,
// error and Stringer attribute before passing the record on. Install it as
// the default handler at startup and call Use once the key store is open.
type Handler struct {
	next slog.Handler
	r    *atomic.Pointer[Redactor]
}

// NewHandler wraps next. Until Use is called it redacts by pattern only.
func NewHandler(next slog.Handler) *Handler {
	return &Handler{next: next, r: &atomic.Pointer[Redactor]{}}
}

// Use sets the Redactor whose known secrets the handler also removes.
func (h *Handler) Use(r *Redactor) { h.r.Store(r) }

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	r := h.r.Load()
	out := slog.NewRecord(rec.Time, rec.Level, r.String(ctx, rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(ctx, r, a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *Handler) attr(ctx context.Context, r *Redactor, a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, r.String(ctx, v.String()))
	case slog.KindGroup:
		as := v.Group()
		out := make([]any, 0, len(as))
		for _, g := range as {
			out = append(out, h.attr(ctx, r, g))
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, r.String(ctx, x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, r.String(ctx, x.String()))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	r := h.r.Load()
	red := make([]slog.Attr, len(as))
	for i, a := range as {
		red[i] = h.attr(context.Background(), r, a)
	}
	return &Handler{next: h.next.WithAttrs(red), r: h.r}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), r: h.r}
}
