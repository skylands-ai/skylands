package cli

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestConsoleLogReadsAtAGlance(t *testing.T) {
	at := time.Date(2026, 11, 5, 11, 2, 44, 985_000_000, time.Local)
	record := func(level slog.Level, msg string, attrs ...slog.Attr) slog.Record {
		r := slog.NewRecord(at, level, msg, 0)
		r.AddAttrs(attrs...)
		return r
	}
	for _, c := range []struct {
		name string
		h    func(*consoleHandler) slog.Handler
		r    slog.Record
		want string
	}{{
		name: "fields after the message",
		r:    record(slog.LevelInfo, "citadel: connected to NATS", slog.String("url", "nats://127.0.0.1:4222"), slog.String("domain", "world")),
		want: "11:02:44 INFO citadel: connected to NATS  url=nats://127.0.0.1:4222 domain=world\n",
	}, {
		name: "no fields",
		r:    record(slog.LevelWarn, "daemon: island down"),
		want: "11:02:44 WARN daemon: island down\n",
	}, {
		name: "quoted values",
		r:    record(slog.LevelError, "agents: failed", slog.Any("err", errors.New("no such file")), slog.String("note", ""), slog.String("q", `a"b`), slog.Int("repos", 0)),
		want: `11:02:44 ERROR agents: failed  err="no such file" note="" q="a\"b" repos=0` + "\n",
	}, {
		name: "durations and times",
		r:    record(slog.LevelInfo, "took", slog.Duration("wait", 1500*time.Millisecond), slog.Time("at", time.Date(2026, 11, 5, 16, 2, 44, 0, time.UTC))),
		want: "11:02:44 INFO took  wait=1.5s at=2026-11-05T16:02:44Z\n",
	}, {
		name: "groups",
		h: func(h *consoleHandler) slog.Handler {
			return h.WithAttrs([]slog.Attr{slog.String("host", "demo")}).WithGroup("nats")
		},
		r:    record(slog.LevelInfo, "leaf up", slog.Int("port", 7422), slog.Group("store", slog.String("dir", "/d")), slog.Group("empty")),
		want: "11:02:44 INFO leaf up  host=demo nats.port=7422 nats.store.dir=/d\n",
	}} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			var h slog.Handler = newConsoleHandler(&buf, slog.LevelInfo)
			if c.h != nil {
				h = c.h(h.(*consoleHandler))
			}
			if err := h.Handle(context.Background(), c.r); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestConsoleLogDropsDebug(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(newConsoleHandler(&buf, slog.LevelInfo))
	log.Debug("noise")
	log.Info("kept")
	if got := buf.String(); len(got) < 14 || got[8:] != " INFO kept\n" {
		t.Errorf("log = %q, want only the info line", got)
	}
}

func TestReadableLogOnlyOnATerminal(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("a buffer is a terminal")
	}
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Error("a regular file is a terminal")
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no terminal to test against")
	}
	defer func() { _ = tty.Close() }()
	if !isTerminal(tty) {
		t.Error("/dev/tty isn't a terminal")
	}
}
