package placement

import (
	"context"

	"github.com/skylands-ai/skylands/internal/daemon/session"
	"github.com/skylands-ai/skylands/internal/dispatch"
)

// Replayed is how the island would have placed one past session's first
// prompt, had it been asked at the island.
type Replayed struct {
	Session session.Session
	Verdict dispatch.Verdict
}

// Replay places every past session's first prompt as if it had been asked
// at this island, scored without the session's own title, which would
// otherwise give its repository away. Each session is a labeled example:
// its prompt, and the repository it ran in. Sessions on repositories no
// longer registered are left out.
func (p *Placer) Replay(ctx context.Context) []Replayed {
	entries := p.Entries(ctx)
	known := map[string]bool{}
	for _, e := range entries {
		known[e.ID] = true
	}
	titles := p.titles()
	var out []Replayed
	for _, s := range p.sessions.List() {
		if s.Prompt == "" || !known[s.RepoID] {
			continue
		}
		ranked := dispatch.Rank(entries, dispatch.ParsePrompt(s.Prompt), titles, s.ID)
		out = append(out, Replayed{Session: s, Verdict: dispatch.Decide([]dispatch.Answer{{Host: p.host, Candidates: ranked}})})
	}
	return out
}
