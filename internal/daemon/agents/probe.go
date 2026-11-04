package agents

import (
	"context"
	"time"

	"github.com/spachava753/acp-sdk/acp"

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
	// ModelsRead says a session opened to read the agent's models, which
	// are then Models, in the agent's order, with none for an agent that
	// has no model setting. DefaultModel is the one the session started on.
	ModelsRead   bool
	Models       []v1.AgentModel
	DefaultModel string
	// ModelsErr is why no session opened to read the models, when they
	// were asked for and the agent did not say it was logged out.
	ModelsErr error
}

// ProbeOptions say what ProbeAgent waits for and reads.
type ProbeOptions struct {
	// AuthWait is how long an agent that reports its login is given to.
	AuthWait time.Duration
	// Models opens a session to read the models the agent offers its
	// login, from the session's ACP config option in the "model"
	// category. Nothing is prompted, so no model is asked anything, but
	// the agent starts what a session needs, such as its MCP servers. An
	// agent that says it is logged out is not asked.
	Models bool
}

// ProbeAgent launches the adapter's agent in cwd, initializes it over ACP,
// reads its models when asked, and stops it. No prompt is sent, so no
// model is asked anything.
func ProbeAgent(ctx context.Context, ad Adapter, cwd string, opts ProbeOptions) (Probe, error) {
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
		case <-time.After(opts.AuthWait):
		case <-ctx.Done():
			return p, ctx.Err()
		}
	}
	if opts.Models && (p.Auth == nil || p.Auth.Kind != "none") {
		p.ModelsErr = r.readModels(ctx, cwd, init, &p)
	}
	return p, nil
}

// readModels opens a session, reads its model setting into p, and closes
// the session when the agent can.
func (r *Run) readModels(ctx context.Context, cwd string, init *acp.InitializeResponse, p *Probe) error {
	sess, err := r.client.NewSession(ctx, &acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return r.fail("open a session", err)
	}
	if caps := init.AgentCapabilities; caps != nil && caps.SessionCapabilities != nil && caps.SessionCapabilities.Close != nil {
		defer func() { _, _ = r.client.CloseSession(ctx, &acp.CloseSessionRequest{SessionID: sess.SessionID}) }()
	}
	p.ModelsRead = true
	var options []acp.SessionConfigOption
	if sess.ConfigOptions != nil {
		options = *sess.ConfigOptions
	}
	o, ok := optionFor(options, acp.SessionConfigOptionCategoryModel)
	if !ok {
		return nil
	}
	p.DefaultModel = optionValue(o)
	p.Models = optionModels(o)
	return nil
}

// optionModels are a model option's choices, in or out of groups.
func optionModels(o acp.SessionConfigOption) []v1.AgentModel {
	var choices []acp.SessionConfigSelectOption
	if o.Options.Ungrouped != nil {
		choices = append(choices, *o.Options.Ungrouped...)
	}
	if o.Options.Groups != nil {
		for _, g := range *o.Options.Groups {
			choices = append(choices, g.Options...)
		}
	}
	var out []v1.AgentModel
	for _, c := range choices {
		m := v1.AgentModel{ID: string(c.Value), Name: c.Name}
		if c.Description != nil {
			m.Description = *c.Description
		}
		out = append(out, m)
	}
	return out
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
