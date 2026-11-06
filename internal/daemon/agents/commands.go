package agents

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Why a prompt that starts with a slash command is refused.
var (
	ErrCommandWithheld = errors.New("the host doesn't run this command: it acts outside the session's tool calls, where no approval reaches it")
	ErrCommandUnknown  = errors.New("this isn't one of the commands the agent announced")
)

// commandWord is a prompt's first word when it names a slash command: a
// slash, then letters, digits, dashes, underscores, and colons, so a path
// such as /usr/bin is not one.
var commandWord = regexp.MustCompile(`^/([A-Za-z0-9_:-]+)$`)

// CommandOf returns the name of the slash command a prompt starts with,
// without the slash, or false when it doesn't start with one.
func CommandOf(prompt string) (string, bool) {
	words := strings.Fields(prompt)
	if len(words) == 0 {
		return "", false
	}
	m := commandWord.FindStringSubmatch(words[0])
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Withholds says whether the adapter withholds the named command. Names
// compare without regard to case, as an agent may match them that way.
func (ad Adapter) Withholds(name string) bool {
	return slices.ContainsFunc(ad.Withheld, func(w string) bool { return strings.EqualFold(w, name) })
}

// CheckPrompt refuses a prompt that starts with a slash command the
// adapter withholds. It needs no agent running.
func (ad Adapter) CheckPrompt(prompt string) error {
	if name, ok := CommandOf(prompt); ok && ad.Withholds(name) {
		return fmt.Errorf("/%s: %w", name, ErrCommandWithheld)
	}
	return nil
}

// Commands are the slash commands the agent announced for the
// conversation, less the ones the adapter withholds, and whether it has
// announced any yet.
func (r *Run) Commands() (commands []v1.AgentCommand, announced bool) {
	r.mu.Lock()
	id := r.session
	r.mu.Unlock()
	r.handler.mu.Lock()
	all, announced := r.handler.commands[id]
	r.handler.mu.Unlock()
	out := []v1.AgentCommand{}
	for _, c := range all {
		if !r.withholds(c.Name) {
			out = append(out, c)
		}
	}
	return out, announced
}

// CheckPrompt refuses a prompt that starts with a slash command the
// adapter withholds, or one the agent didn't announce for the
// conversation. An agent announces its commands just after a session
// opens, so a prompt that comes before them waits up to wait for them.
func (r *Run) CheckPrompt(ctx context.Context, prompt string, wait time.Duration) error {
	name, ok := CommandOf(prompt)
	if !ok {
		return nil
	}
	if r.withholds(name) {
		return fmt.Errorf("/%s: %w", name, ErrCommandWithheld)
	}
	r.mu.Lock()
	id := r.session
	r.mu.Unlock()
	r.handler.mu.Lock()
	announced := r.handler.announcedFor(id)
	r.handler.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-announced:
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	commands, _ := r.Commands()
	if !slices.ContainsFunc(commands, func(c v1.AgentCommand) bool { return strings.EqualFold(c.Name, name) }) {
		return fmt.Errorf("/%s: %w", name, ErrCommandUnknown)
	}
	return nil
}

func (r *Run) withholds(name string) bool {
	return slices.ContainsFunc(r.withheld, func(w string) bool { return strings.EqualFold(w, name) })
}
