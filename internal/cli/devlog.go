package cli

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// consoleHandler writes log records for a person watching a terminal, as
// devworld's are: the local time of day, the level, and the message, then
// the record's fields as key=value, set off by two spaces:
//
//	11:02:44 INFO citadel: connected to NATS  url=nats://127.0.0.1:4222
//
// A value is quoted where slog's text handler would quote it.
type consoleHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Leveler
	// attrs are the fields WithAttrs added, formatted, each after a space.
	attrs string
	// prefix holds the groups WithGroup opened, as "a.b.".
	prefix string
}

func newConsoleHandler(w io.Writer, level slog.Leveler) *consoleHandler {
	return &consoleHandler{mu: &sync.Mutex{}, w: w, level: level}
}

func (h *consoleHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// Handle writes the record as one line, in one write, so lines from the
// citadel and the daemon never interleave.
func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if !r.Time.IsZero() {
		b.WriteString(r.Time.Local().Format(time.TimeOnly))
		b.WriteByte(' ')
	}
	b.WriteString(r.Level.String())
	b.WriteByte(' ')
	b.WriteString(r.Message)
	var fields strings.Builder
	fields.WriteString(h.attrs)
	r.Attrs(func(a slog.Attr) bool {
		appendField(&fields, h.prefix, a)
		return true
	})
	if fields.Len() > 0 {
		b.WriteByte(' ')
		b.WriteString(fields.String())
	}
	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var b strings.Builder
	b.WriteString(h.attrs)
	for _, a := range attrs {
		appendField(&b, h.prefix, a)
	}
	next := *h
	next.attrs = b.String()
	return &next
}

func (h *consoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := *h
	next.prefix += name + "."
	return &next
}

// appendField writes a as " key=value", or a group's fields with their
// keys qualified by the group's, and nothing for an empty attr or group.
func appendField(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			prefix += a.Key + "."
		}
		for _, g := range a.Value.Group() {
			appendField(b, prefix, g)
		}
		return
	}
	b.WriteByte(' ')
	b.WriteString(quoted(prefix + a.Key))
	b.WriteByte('=')
	if a.Value.Kind() == slog.KindTime {
		b.WriteString(quoted(a.Value.Time().Format(time.RFC3339Nano)))
		return
	}
	b.WriteString(quoted(a.Value.String()))
}

// quoted is s, or s quoted when it is empty or holds a space, an equals
// sign, a quote, or anything unprintable.
func quoted(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if r == ' ' || r == '=' || r == '"' || !unicode.IsPrint(r) {
			return strconv.Quote(s)
		}
	}
	return s
}
