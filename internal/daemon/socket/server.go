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

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/host"
	"github.com/skylands-ai/skylands/internal/daemon/session"
	"github.com/skylands-ai/skylands/internal/daemon/terrain"
)

// API is what the daemon exposes to the local CLI.
type API interface {
	Status(ctx context.Context) StatusInfo
	RegisterRepo(ctx context.Context, path, commandID string) (RepoInfo, error)
	// Requests lists every tool call waiting on a person.
	Requests(ctx context.Context) []RequestInfo
	Approve(ctx context.Context, requestID, commandID string) (RequestInfo, error)
	Deny(ctx context.Context, requestID, commandID string) (RequestInfo, error)
	// Sessions lists every session on this host, newest first.
	Sessions(ctx context.Context) []SessionInfo
	StopSession(ctx context.Context, sessionID, commandID string) (SessionInfo, error)
	// CancelTurn interrupts the turn in progress and leaves the session open.
	CancelTurn(ctx context.Context, sessionID, commandID string) (SessionInfo, error)
	// RemoveSession deletes a finished session's worktree and branch.
	RemoveSession(ctx context.Context, sessionID, commandID string, force bool) (RemoveResult, error)
	// PruneSessions removes what finished sessions left behind.
	PruneSessions(ctx context.Context) []PruneResult
	// Questions lists every question waiting on a person.
	Questions(ctx context.Context) []QuestionInfo
	AnswerQuestion(ctx context.Context, questionID, commandID string, answers json.RawMessage, decline bool) (QuestionInfo, error)
	// Rules lists every repository's rules.
	Rules(ctx context.Context) []RepoRulesInfo
	SetRepoRules(ctx context.Context, repoID, commandID string, rules []v1.Rule) (RepoRulesInfo, error)
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

	mux.HandleFunc("GET /v1/requests", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, api.Requests(r.Context()))
	})
	decide := func(do func(ctx context.Context, requestID, commandID string) (RequestInfo, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var req DecideRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			info, err := do(r.Context(), r.PathValue("id"), req.CommandID)
			switch {
			case errors.Is(err, session.ErrUnknownRequest):
				reply(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
			case err != nil:
				reply(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			default:
				reply(w, http.StatusOK, info)
			}
		}
	}
	mux.HandleFunc("POST /v1/requests/{id}/approve", decide(api.Approve))
	mux.HandleFunc("POST /v1/requests/{id}/deny", decide(api.Deny))

	mux.HandleFunc("GET /v1/questions", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, api.Questions(r.Context()))
	})
	mux.HandleFunc("POST /v1/questions/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		var req AnswerRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		info, err := api.AnswerQuestion(r.Context(), r.PathValue("id"), req.CommandID, req.Answers, req.Decline)
		switch {
		case errors.Is(err, session.ErrUnknownQuestion):
			reply(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		case err != nil:
			reply(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		default:
			reply(w, http.StatusOK, info)
		}
	})
	mux.HandleFunc("GET /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, api.Sessions(r.Context()))
	})
	sessionStatus := func(err error) int {
		switch {
		case errors.Is(err, session.ErrUnknownSession):
			return http.StatusNotFound
		case errors.Is(err, session.ErrOpen), errors.Is(err, session.ErrInCheckout):
			return http.StatusConflict
		default:
			return http.StatusBadRequest
		}
	}
	mux.HandleFunc("POST /v1/sessions/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		var req SessionCommand
		_ = json.NewDecoder(r.Body).Decode(&req)
		info, err := api.StopSession(r.Context(), r.PathValue("id"), req.CommandID)
		if err != nil {
			reply(w, sessionStatus(err), ErrorResponse{Error: err.Error()})
			return
		}
		reply(w, http.StatusOK, info)
	})
	mux.HandleFunc("POST /v1/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var req SessionCommand
		_ = json.NewDecoder(r.Body).Decode(&req)
		info, err := api.CancelTurn(r.Context(), r.PathValue("id"), req.CommandID)
		if err != nil {
			reply(w, sessionStatus(err), ErrorResponse{Error: err.Error()})
			return
		}
		reply(w, http.StatusOK, info)
	})
	mux.HandleFunc("DELETE /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req SessionCommand
		_ = json.NewDecoder(r.Body).Decode(&req)
		res, err := api.RemoveSession(r.Context(), r.PathValue("id"), req.CommandID, req.Force)
		if err != nil {
			reply(w, sessionStatus(err), ErrorResponse{Error: err.Error()})
			return
		}
		reply(w, http.StatusOK, res)
	})
	mux.HandleFunc("POST /v1/sessions/prune", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, api.PruneSessions(r.Context()))
	})

	mux.HandleFunc("GET /v1/rules", func(w http.ResponseWriter, r *http.Request) {
		reply(w, http.StatusOK, api.Rules(r.Context()))
	})
	mux.HandleFunc("PUT /v1/repos/{id}/rules", func(w http.ResponseWriter, r *http.Request) {
		var req SetRulesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			reply(w, http.StatusBadRequest, ErrorResponse{Error: "a list of rules is required"})
			return
		}
		info, err := api.SetRepoRules(r.Context(), r.PathValue("id"), req.CommandID, req.Rules)
		switch {
		case errors.Is(err, host.ErrUnknownRepo):
			reply(w, http.StatusNotFound, ErrorResponse{Error: err.Error()})
		case err != nil:
			reply(w, http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		default:
			reply(w, http.StatusOK, info)
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
