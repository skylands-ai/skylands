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
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	v1 "github.com/skylands-ai/skylands/api/v1"
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
	// API, if set, serves every path under /api/.
	API http.Handler
	// Live, if set, starts delivering one session's transcript appends and
	// returns how to stop.
	Live func(host, id string, deliver func(v1.Live)) (stop func(), err error)
	// NeedsSetup, if set and true, sends the world's page to /setup,
	// because there is no world to show yet.
	NeedsSetup func() bool
	// Canonical is the URL people open the citadel at. A page asked for
	// through another loopback name, such as localhost for 127.0.0.1, is
	// sent there, so a browser always finds what it keeps for this origin.
	Canonical string
	// SignedIn, if set, says who a WebSocket request signed in as, which
	// Drop is later asked about, or the status and reason to refuse it with.
	SignedIn func(r *http.Request) (who string, status int, why string)
}

// StatusSignIn closes a browser's WebSocket when what it signed in as no
// longer lets it see the world, so it signs in again and finds out why.
const StatusSignIn websocket.StatusCode = 4401

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
	if cfg.API != nil {
		mux.Handle("/api/", cfg.API)
	}
	mux.Handle("/", s.pages(s.staticHandler()))
	s.mux = mux
	return s
}

// Handler is the server's routes, for tests and for embedding under another
// mux.
func (s *Server) Handler() http.Handler { return s.mux }

// Broadcast sends m to every connected browser.
func (s *Server) Broadcast(m Message) { s.hub.broadcast(m) }

// Drop closes, with StatusSignIn, every browser's WebSocket whose sign-in
// keep refuses, such as an approver just revoked.
func (s *Server) Drop(keep func(who string) bool) { s.hub.drop(keep) }

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
	var who string
	if s.cfg.SignedIn != nil {
		var status int
		var why string
		if who, status, why = s.cfg.SignedIn(r); status != 0 {
			http.Error(w, why, status)
			return
		}
	}
	// Only this citadel's own pages may connect. A browser sends the
	// sign-in cookie with a socket opened from any page on the same site,
	// such as another port on 127.0.0.1, so the origin has to match too.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.Debug("web: websocket accept", "remote", r.RemoteAddr, "err", err)
		return
	}
	defer func() { _ = c.CloseNow() }()
	s.hub.add(c, who)
	defer s.hub.remove(c)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// A browser asks to watch one session at a time. Its reads also keep
	// the connection's control frames flowing.
	var (
		watchMu sync.Mutex
		unwatch func()
	)
	setWatch := func(stop func()) {
		watchMu.Lock()
		defer watchMu.Unlock()
		if unwatch != nil {
			unwatch()
		}
		unwatch = stop
	}
	defer setWatch(nil)
	go func() {
		defer cancel()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var in Inbound
			if json.Unmarshal(data, &in) != nil {
				continue
			}
			switch in.Type {
			case "watch":
				if s.cfg.Live == nil {
					continue
				}
				host, id := in.Host, in.ID
				stop, err := s.cfg.Live(host, id, func(l v1.Live) {
					if err := sendJSON(ctx, c, Message{Type: "live", Host: host, ID: id, Live: &l}); err != nil {
						cancel()
					}
				})
				if err != nil {
					slog.Debug("web: watch refused", "host", host, "session", id, "err", err)
					continue
				}
				setWatch(stop)
			case "unwatch":
				setWatch(nil)
			}
		}
	}()

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
// pages sends a page asked for through another loopback name to the
// canonical one, the world's page to setup while there is no world, and
// /setup to the setup page.
func (s *Server) pages(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if to := s.loopbackRedirect(r); to != "" {
				http.Redirect(w, r, to, http.StatusTemporaryRedirect)
				return
			}
			if (r.URL.Path == "/" || r.URL.Path == "/index.html") && s.cfg.NeedsSetup != nil && s.cfg.NeedsSetup() {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
			if r.URL.Path == "/setup" {
				r = r.Clone(r.Context())
				r.URL.Path = "/setup.html"
			}
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackRedirect is where to send a page request that reached the
// citadel through a loopback name other than the canonical one, or "".
func (s *Server) loopbackRedirect(r *http.Request) string {
	u, err := url.Parse(s.cfg.Canonical)
	if err != nil || u.Host == "" || strings.HasPrefix(r.URL.Path, "/assets/") {
		return ""
	}
	if !isLoopback(u.Hostname()) || !isLoopback(hostOnly(r.Host)) || strings.EqualFold(r.Host, u.Host) {
		return ""
	}
	_, reqPort, _ := net.SplitHostPort(r.Host)
	if reqPort != u.Port() {
		return ""
	}
	return u.Scheme + "://" + u.Host + r.URL.RequestURI()
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) staticHandler() http.Handler {
	if s.cfg.Dist != nil {
		if _, err := fs.Stat(s.cfg.Dist, "index.html"); err == nil {
			files := http.FileServerFS(s.cfg.Dist)
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Vite names assets by content hash, so they can be cached for
				// good. The page that references them must always be fresh.
				if strings.HasPrefix(r.URL.Path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
			})
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

// ReleasesURL is where release binaries, which have the web client built
// in, are published.
const ReleasesURL = "https://github.com/skylands-ai/skylands/releases"

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
  <p>The citadel is running, but the web client has not been built into this binary.
  A binary from <code>go install</code> never has it, because npm builds the client.</p>
  <p>Use a <a href="` + ReleasesURL + `">release binary</a>, or in a clone of the
  repository run <code>make web &amp;&amp; make build</code> and start the citadel again.</p>
</main>
</body>
</html>
`
