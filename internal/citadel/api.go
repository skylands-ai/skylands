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
	// InCheckout runs the agent in the registered checkout itself rather
	// than a worktree of its own.
	InCheckout bool `json:"inCheckout"`
}

// api is the citadel's HTTP API for browsers: the commands the world view
// can send. Each one is relayed to the daemon that owns the target by
// request-reply, and the daemon's answer comes back.
func (c *citadel) api() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions", c.startSession)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/prompt", c.sendPrompt)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/cancel", c.cancelTurn)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/stop", c.stopAgent)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/requests/{rid}/approve", c.approve)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/requests/{rid}/deny", c.deny)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/questions/{qid}/answer", c.answer)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/trust", c.trust)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/rules", c.sessionRules)
	mux.HandleFunc("POST /api/repos/{host}/{id}/rules", c.repoRules)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/commit", c.commit)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/merge", c.merge)
	mux.HandleFunc("POST /api/sessions/{host}/{id}/pull-request", c.pullRequest)
	mux.HandleFunc("GET /api/sessions/{host}/{id}/transcript", c.fetchFromHost(v1.FetchTranscript))
	mux.HandleFunc("GET /api/sessions/{host}/{id}/diff", c.fetchFromHost(v1.FetchDiff))
	mux.HandleFunc("GET /api/sessions/{host}/{id}/changes", c.fetchFromHost(v1.FetchChanges))
	return mux
}

// rulesBody is the body of the rule endpoints: the whole list.
type rulesBody struct {
	Rules []v1.Rule `json:"rules"`
}

// sessionRules replaces a session's rules on its island.
func (c *citadel) sessionRules(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	var body rulesBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Rules == nil {
		writeError(w, http.StatusBadRequest, "a list of rules is required")
		return
	}
	cmd := v1.SetSessionRules{ID: nuid.Next(), SessionID: id, Rules: body.Rules}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandSetSessionRules), cmd, http.StatusOK)
}

// repoRules replaces a repository's rules on its island, which sessions
// started on it from then on copy.
func (c *citadel) repoRules(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	var body rulesBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Rules == nil {
		writeError(w, http.StatusBadRequest, "a list of rules is required")
		return
	}
	cmd := v1.SetRepoRules{ID: nuid.Next(), RepoID: id, Rules: body.Rules}
	c.relay(w, host, v1.RepoCommandSubject(host, id, v1.CommandSetRepoRules), cmd, http.StatusOK)
}

// commit commits everything in a session's worktree on its island.
func (c *citadel) commit(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	cmd := v1.CommitChanges{ID: nuid.Next(), SessionID: id, Message: strings.TrimSpace(body.Message)}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandCommitChanges), cmd, http.StatusOK)
}

// merge merges a session's branch into its base in the checkout on its island.
func (c *citadel) merge(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	cmd := v1.MergeChanges{ID: nuid.Next(), SessionID: id}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandMergeChanges), cmd, http.StatusOK)
}

// pullRequest pushes a session's branch and opens a pull request from it.
func (c *citadel) pullRequest(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	cmd := v1.OpenPullRequest{ID: nuid.Next(), SessionID: id, Title: strings.TrimSpace(body.Title), Body: strings.TrimSpace(body.Body)}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandOpenPullRequest), cmd, http.StatusOK)
}

// sendPrompt continues an idle session on its island.
func (c *citadel) sendPrompt(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeError(w, http.StatusBadRequest, "a prompt is required")
		return
	}
	cmd := v1.SendPrompt{ID: nuid.Next(), SessionID: id, Text: strings.TrimSpace(body.Text)}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandSendPrompt), cmd, http.StatusOK)
}

// cancelTurn interrupts a session's turn on its island and leaves the
// session open.
func (c *citadel) cancelTurn(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	cmd := v1.CancelTurn{ID: nuid.Next(), SessionID: id}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandCancelTurn), cmd, http.StatusOK)
}

// stopAgent ends a session on its island.
func (c *citadel) stopAgent(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	cmd := v1.StopAgent{ID: nuid.Next(), SessionID: id}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandStopAgent), cmd, http.StatusOK)
}

// approve answers a waiting tool call with an allow option, the agent's
// first unless the body names one.
func (c *citadel) approve(w http.ResponseWriter, r *http.Request) {
	host, id, rid := r.PathValue("host"), r.PathValue("id"), r.PathValue("rid")
	var body struct {
		OptionID string `json:"optionId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	cmd := v1.ApproveToolCall{ID: nuid.Next(), SessionID: id, RequestID: rid, OptionID: body.OptionID}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandApproveToolCall), cmd, http.StatusOK)
}

// deny answers a waiting tool call with a reject option.
func (c *citadel) deny(w http.ResponseWriter, r *http.Request) {
	host, id, rid := r.PathValue("host"), r.PathValue("id"), r.PathValue("rid")
	cmd := v1.DenyToolCall{ID: nuid.Next(), SessionID: id, RequestID: rid}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandDenyToolCall), cmd, http.StatusOK)
}

// answer gives a waiting question its answers, values by field key, or
// declines it.
func (c *citadel) answer(w http.ResponseWriter, r *http.Request) {
	host, id, qid := r.PathValue("host"), r.PathValue("id"), r.PathValue("qid")
	var body struct {
		Answers json.RawMessage `json:"answers"`
		Decline bool            `json:"decline"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (!body.Decline && len(body.Answers) == 0) {
		writeError(w, http.StatusBadRequest, "answers by field, or decline, are required")
		return
	}
	cmd := v1.AnswerQuestion{ID: nuid.Next(), SessionID: id, QuestionID: qid, Answers: body.Answers, Decline: body.Decline}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandAnswerQuestion), cmd, http.StatusOK)
}

// trust allows a session's tool calls without asking, or asks again.
func (c *citadel) trust(w http.ResponseWriter, r *http.Request) {
	host, id := r.PathValue("host"), r.PathValue("id")
	var body struct {
		Trusted bool `json:"trusted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "trusted: true or false is required")
		return
	}
	cmd := v1.TrustSession{ID: nuid.Next(), SessionID: id, Trusted: body.Trusted}
	c.relay(w, host, v1.SessionCommandSubject(host, id, v1.CommandTrustSession), cmd, http.StatusOK)
}

// relay sends a command to a host and writes the daemon's result, or an
// error with a status that says whose fault it was.
func (c *citadel) relay(w http.ResponseWriter, host, subject string, cmd any, okStatus int) {
	result, err := c.sendCommand(context.Background(), subject, cmd)
	switch {
	case errors.Is(err, nats.ErrNoResponders), errors.Is(err, nats.ErrTimeout):
		writeError(w, http.StatusBadGateway, host+" did not answer")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(okStatus)
	_, _ = w.Write(result)
}

// fetchFromHost relays an on-demand read of one session's transcript,
// diff, or changes from the host that holds it. The citadel keeps nothing
// of the answer.
func (c *citadel) fetchFromHost(kind string) http.HandlerFunc {
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
	cmd := v1.StartSession{ID: nuid.Next(), SessionID: sessionID, RepoID: req.RepoID, Agent: req.Agent, Model: strings.TrimSpace(req.Model), Prompt: req.Prompt, InCheckout: req.InCheckout}
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
