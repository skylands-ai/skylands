package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/daemon/terrain"
)

// API is what the daemon exposes to the local CLI.
type API interface {
	Status(ctx context.Context) StatusInfo
	RegisterRepo(ctx context.Context, path, commandID string) (RepoInfo, error)
}

// Serve listens on the Unix socket at path until ctx ends. A stale socket
// file from an earlier daemon is replaced, and the socket is removed on
// exit. Only the owning user can connect.
func Serve(ctx context.Context, path string, api API) error {
	// Unix socket paths are limited to about 104 bytes on macOS and 108 on
	// Linux, and the OS error for a long one is unhelpful.
	if len(path) > 100 {
		return fmt.Errorf("socket path %s is %d bytes long; the limit is about 104, so choose a shorter --socket", path, len(path))
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("protect socket %s: %w", path, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, api.Status(r.Context()))
	})
	mux.HandleFunc("GET /v1/repos", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, api.Status(r.Context()).Repos)
	})
	mux.HandleFunc("POST /v1/repos", func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
			reply(w, http.StatusBadRequest, ErrorResponse{Error: "a repository path is required"})
			return
		}
		repo, err := api.RegisterRepo(r.Context(), req.Path, req.CommandID)
		switch {
		case errors.Is(err, host.ErrAlreadyRegistered):
			reply(w, http.StatusConflict, ErrorResponse{Error: fmt.Sprintf("%s is already registered as %s", repo.Path, repo.Name)})
		case errors.Is(err, terrain.ErrNotRepository), errors.Is(err, os.ErrNotExist):
			reply(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		case err != nil:
			reply(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		default:
			reply(w, http.StatusCreated, repo)
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	slog.Info("daemon: local API ready", "socket", path)
	select {
	case err := <-errc:
		_ = os.Remove(path)
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		if serveErr := <-errc; !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		_ = os.Remove(path)
		return err
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
