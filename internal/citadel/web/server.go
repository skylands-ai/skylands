// Package web is the citadel's HTTP and WebSocket server for browsers. It
// serves the built client and streams the citadel's view of the world to
// each browser as JSON messages.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// Config describes what the server listens on and serves.
type Config struct {
	// Addr is the host:port to listen on.
	Addr string
	// Snapshot returns the message that opens every browser connection: the
	// world as the citadel sees it right now.
	Snapshot func() Message
	// Dist is the built web client with index.html at its root. When it is
	// nil or has no index.html, the server serves a placeholder page that
	// says how to build the client.
	Dist fs.FS
}

// Server serves the web client and the WebSocket browsers subscribe to.
type Server struct {
	cfg Config
	hub *hub
	mux *http.ServeMux
}

// New builds a server from cfg. Call Run or Serve to start it.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg, hub: newHub()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.Handle("/", s.staticHandler())
	s.mux = mux
	return s
}

// Handler is the server's routes, for tests and for embedding under another
// mux.
func (s *Server) Handler() http.Handler { return s.mux }

// Broadcast sends m to every connected browser.
func (s *Server) Broadcast(m Message) { s.hub.broadcast(m) }

// Run listens on the configured address and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve serves on ln until ctx is cancelled, then shuts down cleanly. A
// cancelled context is a normal stop, not an error.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	slog.Info("web: listening", "url", "http://"+ln.Addr().String())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		s.hub.closeAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if serveErr := <-errc; !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*"},
	})
	if err != nil {
		slog.Debug("web: websocket accept", "remote", r.RemoteAddr, "err", err)
		return
	}
	defer func() { _ = c.CloseNow() }()
	s.hub.add(c)
	defer s.hub.remove(c)

	// Browsers only listen for now, so reads are discarded and the returned
	// context ends when the browser goes away.
	ctx := c.CloseRead(r.Context())

	if err := sendJSON(ctx, c, s.cfg.Snapshot()); err != nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func sendJSON(ctx context.Context, c *websocket.Conn, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, data)
}

// staticHandler serves the built client, or the placeholder when there is
// no build.
func (s *Server) staticHandler() http.Handler {
	if s.cfg.Dist != nil {
		if _, err := fs.Stat(s.cfg.Dist, "index.html"); err == nil {
			return http.FileServerFS(s.cfg.Dist)
		} else if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("web: client build unreadable, serving placeholder", "err", err)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, placeholderHTML)
	})
}

const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Skylands</title>
<style>
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; font-family: system-ui, sans-serif; background: #E6EDF4; color: #13202E; }
  main { max-width: 32rem; padding: 2rem; background: #fff; border: 1px solid #D2DCE6; border-radius: 12px; }
  code { font-family: ui-monospace, monospace; background: #F0F4F8; padding: 0.1em 0.4em; border-radius: 4px; }
  @media (prefers-color-scheme: dark) {
    body { background: #101B27; color: #E7EEF4; }
    main { background: #172636; border-color: #2E4459; }
    code { background: #1D3043; }
  }
</style>
</head>
<body>
<main>
  <h1>Skylands</h1>
  <p>The citadel is running, but the web client has not been built into this binary.</p>
  <p>Run <code>make web</code> in the repository, rebuild, and start the citadel again.</p>
</main>
</body>
</html>
`
