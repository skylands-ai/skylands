package session

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nuid"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/agents"
)

// SetMode handles SetSessionMode: it sets an idle session's mode to one
// its adapter lists, through the agent, and records it. by is "local" or
// "citadel". Every listed mode keeps the agent asking before it acts, so
// the change isn't signed, like a prompt.
func (m *Manager) SetMode(ctx context.Context, cmd v1.SetSessionMode, by string) (Session, error) {
	return m.setting(ctx, cmd.ID, cmd.SessionID, by, func(run *agents.Run) error {
		if err := run.SetMode(ctx, cmd.Mode); err != nil {
			return err
		}
		if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventSessionModeChanged, v1.SessionModeChanged{SessionID: cmd.SessionID, Mode: cmd.Mode, By: by}); err != nil {
			return err
		}
		m.note(cmd.SessionID, "Mode set to "+cmd.Mode+".")
		slog.Info("session: mode set", "session", cmd.SessionID, "mode", cmd.Mode, "by", by)
		return nil
	})
}

// SetModel handles SetSessionModel: it sets an idle session's model
// through the agent, and records it. by is as in SetMode.
func (m *Manager) SetModel(ctx context.Context, cmd v1.SetSessionModel, by string) (Session, error) {
	return m.setting(ctx, cmd.ID, cmd.SessionID, by, func(run *agents.Run) error {
		if err := run.SetModel(ctx, cmd.Model); err != nil {
			return err
		}
		if err := m.record(ctx, cmd.SessionID, cmd.ID, v1.EventSessionModelChanged, v1.SessionModelChanged{SessionID: cmd.SessionID, Model: cmd.Model, By: by}); err != nil {
			return err
		}
		m.note(cmd.SessionID, "Model set to "+cmd.Model+".")
		slog.Info("session: model set", "session", cmd.SessionID, "model", cmd.Model, "by", by)
		return nil
	})
}

// setting runs a change to an idle session's settings. A command id
// already in the history gets the session back.
func (m *Manager) setting(ctx context.Context, commandID, id, by string, set func(run *agents.Run) error) (Session, error) {
	m.cmdMu.Lock()
	defer m.cmdMu.Unlock()
	m.mu.Lock()
	if handled, ok := m.handled[commandID]; ok && commandID != "" {
		s := *m.sessions[handled]
		m.mu.Unlock()
		return s, nil
	}
	s, ok := m.sessions[id]
	l := m.live[id]
	if !ok {
		m.mu.Unlock()
		return Session{}, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	switch {
	case s.Status == StatusEnded || s.Status == StatusFailed:
		m.mu.Unlock()
		return *s, ErrOver
	case l == nil && m.resuming[id]:
		m.mu.Unlock()
		return *s, ErrResuming
	case l == nil:
		m.mu.Unlock()
		return *s, ErrOver
	case s.Status != StatusIdle:
		m.mu.Unlock()
		return *s, ErrBusy
	}
	run := l.run
	m.mu.Unlock()
	if err := set(run); err != nil {
		out, _ := m.Get(id)
		return out, err
	}
	out, _ := m.Get(id)
	return out, nil
}

// ModeChanged records the session's mode changing without anyone asking
// the daemon to change it: the agent changed it itself, or the daemon set
// back a mode the adapter doesn't list.
func (o *observer) ModeChanged(mode, by string) {
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventSessionModeChanged, v1.SessionModeChanged{SessionID: o.id, Mode: mode, By: by})
	switch {
	case by == v1.ChangedByDaemon:
		o.m.note(o.id, "Set the mode back to "+mode+".")
	case o.listed(mode):
		o.m.note(o.id, "The agent switched to mode "+mode+".")
	default:
		o.m.note(o.id, "The agent switched to mode "+mode+", in which it may act without asking. Setting it back.")
	}
}

// ModelChanged records the agent changing its own model.
func (o *observer) ModelChanged(model string) {
	_ = o.m.record(o.m.ctx, o.id, "", v1.EventSessionModelChanged, v1.SessionModelChanged{SessionID: o.id, Model: model, By: v1.ChangedByAgent})
	o.m.note(o.id, "The agent switched its model to "+model+".")
}

// ModeLost stops the session: its agent moved to a mode in which it may
// act without asking, and could not be set back.
func (o *observer) ModeLost(err error) {
	slog.Warn("session: stopping, the agent's mode could not be set back", "session", o.id, "err", err)
	o.m.note(o.id, "The agent left the modes that keep it asking and could not be set back, so the session is stopped: "+err.Error())
	_, _ = o.m.Stop(o.m.ctx, v1.StopAgent{ID: nuid.Next(), SessionID: o.id})
}

// listed says whether the session's adapter lists mode.
func (o *observer) listed(mode string) bool {
	s, ok := o.m.Get(o.id)
	if !ok {
		return false
	}
	adapter, ok := o.m.cfg.Registry[s.Agent]
	if !ok {
		return false
	}
	for _, listed := range adapter.Modes {
		if listed == mode {
			return true
		}
	}
	return len(adapter.Modes) == 0
}

// CommandWait is how long a prompt that starts with a slash command waits
// for the agent to announce its commands, which it does just after a
// session opens.
const CommandWait = 5 * time.Second

// Commands are the slash commands a live session's agent announced, less
// the ones its adapter withholds, for a person to pick from. A session
// whose agent isn't up has none.
func (m *Manager) Commands(id string) (v1.CommandsResult, error) {
	m.mu.Lock()
	_, ok := m.sessions[id]
	l := m.live[id]
	m.mu.Unlock()
	if !ok {
		return v1.CommandsResult{}, fmt.Errorf("%w: %s", ErrUnknownSession, id)
	}
	if l == nil {
		return v1.CommandsResult{Commands: []v1.AgentCommand{}}, nil
	}
	commands, announced := l.run.Commands()
	return v1.CommandsResult{Announced: announced, Commands: commands}, nil
}
