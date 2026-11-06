package socket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// ErrNoDaemon means nothing answered on the socket.
var ErrNoDaemon = errors.New("no daemon is listening; start one with \"skylands daemon up\", or point --socket at its socket")

// Client talks to the daemon on this host over its Unix socket.
type Client struct {
	path string
	http *http.Client
}

// Dial prepares a client for the socket at path. Nothing connects until the
// first call.
func Dial(path string) *Client {
	return &Client{
		path: path,
		http: &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		}},
	}
}

// Status reports the daemon's host, world, version, and repositories.
func (c *Client) Status(ctx context.Context) (StatusInfo, error) {
	var out StatusInfo
	err := c.call(ctx, http.MethodGet, "/v1/status", nil, &out)
	return out, err
}

// RegisterRepo registers the repository containing path as a terrace.
func (c *Client) RegisterRepo(ctx context.Context, path string) (RepoInfo, error) {
	var out RepoInfo
	err := c.call(ctx, http.MethodPost, "/v1/repos", RegisterRepoRequest{Path: path, CommandID: nuid.Next()}, &out)
	return out, err
}

// Requests lists every tool call waiting on a person on this host.
func (c *Client) Requests(ctx context.Context) ([]RequestInfo, error) {
	var out []RequestInfo
	err := c.call(ctx, http.MethodGet, "/v1/requests", nil, &out)
	return out, err
}

// History lists every request answered or expired on this host, oldest
// first, in one session or, with "", in all of them.
func (c *Client) History(ctx context.Context, session string) ([]DecisionInfo, error) {
	var out []DecisionInfo
	err := c.call(ctx, http.MethodGet, "/v1/requests/history?session="+url.QueryEscape(session), nil, &out)
	return out, err
}

// Decision is one answered or expired request with the input it named and
// what its tool call produced, as the host kept them, unchecked.
func (c *Client) Decision(ctx context.Context, requestID string) (DecisionDetail, error) {
	var out DecisionDetail
	err := c.call(ctx, http.MethodGet, "/v1/requests/"+url.PathEscape(requestID)+"/decision", nil, &out)
	return out, err
}

// Approve answers a waiting request with the agent's first allow option.
func (c *Client) Approve(ctx context.Context, requestID string) (RequestInfo, error) {
	var out RequestInfo
	err := c.call(ctx, http.MethodPost, "/v1/requests/"+requestID+"/approve", DecideRequest{CommandID: nuid.Next()}, &out)
	return out, err
}

// Deny answers a waiting request with the agent's first reject option.
func (c *Client) Deny(ctx context.Context, requestID string) (RequestInfo, error) {
	var out RequestInfo
	err := c.call(ctx, http.MethodPost, "/v1/requests/"+requestID+"/deny", DecideRequest{CommandID: nuid.Next()}, &out)
	return out, err
}

// Questions lists every question waiting on a person on this host.
func (c *Client) Questions(ctx context.Context) ([]QuestionInfo, error) {
	var out []QuestionInfo
	err := c.call(ctx, http.MethodGet, "/v1/questions", nil, &out)
	return out, err
}

// AnswerQuestion answers a waiting question with values by field key, or
// declines it when answers is nil.
func (c *Client) AnswerQuestion(ctx context.Context, questionID string, answers json.RawMessage) (QuestionInfo, error) {
	var out QuestionInfo
	err := c.call(ctx, http.MethodPost, "/v1/questions/"+questionID+"/answer", AnswerRequest{CommandID: nuid.Next(), Answers: answers, Decline: answers == nil}, &out)
	return out, err
}

// Sessions lists every session on this host, newest first.
func (c *Client) Sessions(ctx context.Context) ([]SessionInfo, error) {
	var out []SessionInfo
	err := c.call(ctx, http.MethodGet, "/v1/sessions", nil, &out)
	return out, err
}

// StopSession ends a session, cancelling any turn in progress.
func (c *Client) StopSession(ctx context.Context, sessionID string) (SessionInfo, error) {
	var out SessionInfo
	err := c.call(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/stop", SessionCommand{CommandID: nuid.Next()}, &out)
	return out, err
}

// CancelTurn interrupts the turn in progress and leaves the session open
// for the next prompt.
func (c *Client) CancelTurn(ctx context.Context, sessionID string) (SessionInfo, error) {
	var out SessionInfo
	err := c.call(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/cancel", SessionCommand{CommandID: nuid.Next()}, &out)
	return out, err
}

// SetMode sets an idle session's mode to one its adapter lists.
func (c *Client) SetMode(ctx context.Context, sessionID, mode string) (SessionInfo, error) {
	var out SessionInfo
	err := c.call(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/mode", SessionCommand{CommandID: nuid.Next(), Value: mode}, &out)
	return out, err
}

// SetModel sets an idle session's model.
func (c *Client) SetModel(ctx context.Context, sessionID, model string) (SessionInfo, error) {
	var out SessionInfo
	err := c.call(ctx, http.MethodPost, "/v1/sessions/"+sessionID+"/model", SessionCommand{CommandID: nuid.Next(), Value: model}, &out)
	return out, err
}

// RemoveSession deletes a finished session's worktree and branch. With
// force, uncommitted changes and unmerged commits go too.
func (c *Client) RemoveSession(ctx context.Context, sessionID string, force bool) (RemoveResult, error) {
	var out RemoveResult
	err := c.call(ctx, http.MethodDelete, "/v1/sessions/"+sessionID, SessionCommand{CommandID: nuid.Next(), Force: force}, &out)
	return out, err
}

// PruneSessions removes the worktrees and branches finished sessions left
// behind, keeping any with uncommitted changes or unmerged commits.
func (c *Client) PruneSessions(ctx context.Context) ([]PruneResult, error) {
	var out []PruneResult
	err := c.call(ctx, http.MethodPost, "/v1/sessions/prune", struct{}{}, &out)
	return out, err
}

// CheckAgents asks the daemon to check now which agents it can launch, in
// its own environment, and returns what it found.
func (c *Client) CheckAgents(ctx context.Context) ([]v1.AgentAvailability, error) {
	var out []v1.AgentAvailability
	err := c.call(ctx, http.MethodPost, "/v1/agents/check", nil, &out)
	return out, err
}

// Rules lists every repository's rules on this host.
func (c *Client) Rules(ctx context.Context) ([]RepoRulesInfo, error) {
	var out []RepoRulesInfo
	err := c.call(ctx, http.MethodGet, "/v1/rules", nil, &out)
	return out, err
}

// SetRepoRules replaces a repository's rules.
func (c *Client) SetRepoRules(ctx context.Context, repoID string, rules []v1.Rule) (RepoRulesInfo, error) {
	var out RepoRulesInfo
	err := c.call(ctx, http.MethodPut, "/v1/repos/"+repoID+"/rules", SetRulesRequest{CommandID: nuid.Next(), Rules: rules}, &out)
	return out, err
}

func (c *Client) call(ctx context.Context, method, route string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://daemon"+route, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			return fmt.Errorf("%w (%s)", ErrNoDaemon, c.path)
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e ErrorResponse
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("daemon replied %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
