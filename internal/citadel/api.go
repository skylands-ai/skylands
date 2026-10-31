package citadel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// startSessionRequest is the body of POST /api/sessions.
type startSessionRequest struct {
	Host   string `json:"host"`
	RepoID string `json:"repoId"`
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// api is the citadel's HTTP API for browsers: the commands the world view
// can send. Each one is relayed to the daemon that owns the target by
// request-reply, and the daemon's answer comes back.
func (c *citadel) api() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", c.startSession)
	mux.HandleFunc("GET /api/sessions/{host}/{id}/transcript", c.fetchText(v1.FetchTranscript))
	mux.HandleFunc("GET /api/sessions/{host}/{id}/diff", c.fetchText(v1.FetchDiff))
	return mux
}

// fetchText relays an on-demand read of one session's transcript or diff
// from the host that holds it. The citadel keeps nothing of the answer.
func (c *citadel) fetchText(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, id := r.PathValue("host"), r.PathValue("id")
		result, err := c.sendCommand(r.Context(), v1.IslandFetchSubject(host, kind), v1.FetchSessionRequest{SessionID: id})
		switch {
		case errors.Is(err, nats.ErrNoResponders), errors.Is(err, nats.ErrTimeout):
			writeError(w, http.StatusBadGateway, host+" did not answer; it may be unreachable")
			return
		case err != nil:
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(result)
	}
}

// startSession dispatches a prompt straight to the island that holds the
// repository. Choosing the host or repo for a work order comes later.
func (c *citadel) startSession(w http.ResponseWriter, r *http.Request) {
	var req startSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return
	}
	req.Host, req.RepoID, req.Agent, req.Prompt = strings.TrimSpace(req.Host), strings.TrimSpace(req.RepoID), strings.TrimSpace(req.Agent), strings.TrimSpace(req.Prompt)
	if req.Host == "" || req.RepoID == "" || req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "host, repoId, and prompt are required")
		return
	}
	if req.Agent == "" {
		req.Agent = "claude"
	}
	if isl, ok := c.islands.Get(req.Host); !ok {
		writeError(w, http.StatusNotFound, "no island named "+req.Host)
		return
	} else if !isl.Reachable {
		writeError(w, http.StatusBadGateway, req.Host+" is unreachable right now; the order will not queue yet")
		return
	}

	sessionID := nuid.Next()
	cmd := v1.StartSession{ID: nuid.Next(), SessionID: sessionID, RepoID: req.RepoID, Agent: req.Agent, Model: strings.TrimSpace(req.Model), Prompt: req.Prompt}
	result, err := c.sendCommand(r.Context(), v1.SessionCommandSubject(req.Host, sessionID, v1.CommandStartSession), cmd)
	switch {
	case errors.Is(err, nats.ErrNoResponders), errors.Is(err, nats.ErrTimeout):
		writeError(w, http.StatusBadGateway, req.Host+" did not answer")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(result)
}

// sendCommand relays one command by request-reply and returns the
// daemon's result, or its error as a Go error.
func (c *citadel) sendCommand(ctx context.Context, subject string, cmd any) (json.RawMessage, error) {
	c.mu.Lock()
	nc := c.nc
	c.mu.Unlock()
	if nc == nil || !nc.IsConnected() {
		return nil, errors.New("the citadel is not connected to NATS")
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nats.ErrTimeout
		}
		return nil, err
	}
	var reply v1.Reply
	if err := json.Unmarshal(msg.Data, &reply); err != nil {
		return nil, fmt.Errorf("bad reply from %s: %w", subject, err)
	}
	if reply.Error != "" {
		return nil, errors.New(reply.Error)
	}
	return reply.Result, nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
