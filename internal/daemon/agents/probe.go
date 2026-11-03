package agents

import (
	"context"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// authStatusMethod is the notification Claude Code's ACP adapter pushes
// with the login the agent uses: once shortly after initialize, then
// whenever it changes. It is an interim extension, carried until ACP has
// a field of its own for it.
const authStatusMethod = "_auth/status_update"

// AuthStatus is the login an agent says it uses. Kind is "account" for a
// Claude subscription, "api_key", "external" for a cloud provider such as
// Bedrock, "gateway", or "none" for logged out.
type AuthStatus struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

type authStatusUpdate struct {
	AuthStatus *AuthStatus `json:"authStatus"`
}

// authStatus keeps the newest login the agent reported.
func (h *handler) authStatus(_ context.Context, u *authStatusUpdate) error {
	if u.AuthStatus == nil {
		return nil
	}
	select {
	case <-h.auth:
	default:
	}
	h.auth <- *u.AuthStatus
	return nil
}

// Probe is what an agent says about itself when it starts.
type Probe struct {
	// Name and Version are what the agent calls itself.
	Name, Version string
	// LoadSession says whether the agent can load a session again, which
	// is how a session survives a daemon restart with the agent's memory.
	LoadSession bool
	// Auth is the login the agent reported, or nil when it reports logins
	// but said nothing before the wait ran out, or does not report them.
	Auth *AuthStatus
	// ReportsAuth says whether the agent reports its login at all.
	ReportsAuth bool
}

// ProbeAgent launches the adapter's agent in cwd, initializes it over ACP,
// and stops it, without opening a session, so no model is asked anything.
// An agent that reports its login is given until wait to do so.
func ProbeAgent(ctx context.Context, ad Adapter, cwd string, wait time.Duration) (Probe, error) {
	r, init, err := launch(ctx, ad, cwd, "", quietObserver{})
	if err != nil {
		return Probe{}, err
	}
	defer func() { _ = r.Close() }()
	p := Probe{}
	p.Name, p.Version = identity(ad, init)
	if caps := init.AgentCapabilities; caps != nil {
		p.LoadSession = caps.LoadSession
		_, p.ReportsAuth = caps.Meta["authStatus"]
	}
	if p.ReportsAuth {
		select {
		case a := <-r.handler.auth:
			p.Auth = &a
		case <-time.After(wait):
		case <-ctx.Done():
			return p, ctx.Err()
		}
	}
	return p, nil
}

// quietObserver ignores everything, for an agent with no session.
type quietObserver struct{}

func (quietObserver) Spawned(string, string, string) {}
func (quietObserver) ToolCall(ToolCall)              {}
func (quietObserver) Plan([]v1.PlanEntry)            {}
func (quietObserver) Usage(uint64, uint64)           {}
func (quietObserver) Tokens(string, v1.TokenUsage)   {}
func (quietObserver) Message(string)                 {}
func (quietObserver) Question(context.Context, Question) Answer {
	return Answer{Cancelled: true}
}
func (quietObserver) Permission(context.Context, PermissionRequest) Decision {
	return Decision{Cancelled: true}
}
