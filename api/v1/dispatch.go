package v1

import "time"

// Work asked for at the world or an island goes where it fits in two
// steps: the citadel picks the island, and the island's daemon picks the
// repository by searching its own, so no code leaves the host to make
// the choice. The citadel asks each island it considers with a match
// fetch, and compares what they answer. A close call between
// repositories on one island goes to that island's agent, with a pick
// fetch. Both are on-demand reads that no stream captures.

// Fetch kinds that place work.
const (
	// FetchMatch scores a prompt against the island's repositories. The
	// request is a MatchRequest and the answer a MatchResult.
	FetchMatch = "match"
	// FetchPick asks the island's agent which of the repositories that
	// scored close a prompt is for. The request is a PickRequest and the
	// answer a PickResult.
	FetchPick = "pick"
)

// MatchTimeout is how long a citadel waits for an island to answer a
// match. An island that takes longer is left out, and the person is told.
const MatchTimeout = 5 * time.Second

// PickTimeout is how long a citadel waits for an island's agent to pick a
// repository, which takes starting the agent and one short answer.
const PickTimeout = 2 * time.Minute

// MaxCandidates is the most candidates a match answer carries.
const MaxCandidates = 5

// MatchRequest asks a daemon which of its repositories a prompt is for.
type MatchRequest struct {
	Prompt string `json:"prompt"`
}

// MatchResult is a daemon's answer to a match: at most MaxCandidates of
// its repositories, best first, leaving out those the prompt doesn't
// touch at all. Repos counts the repositories it scored.
type MatchResult struct {
	Candidates []MatchCandidate `json:"candidates"`
	Repos      int              `json:"repos"`
}

// MatchCandidate is one repository a prompt may be for.
type MatchCandidate struct {
	RepoID string `json:"repoId"`
	Name   string `json:"name"`
	// Confidence is how much of the prompt the repository accounts for,
	// from 0 to 1. Each meaningful word in the prompt adds a fixed weight,
	// set by the best place it matched: a repository or module name most,
	// then a file path, then the README, a commit subject, or an earlier
	// conversation's title. Nothing in it depends on what else is on the
	// island, so confidences from different islands compare.
	Confidence float64 `json:"confidence"`
	// Named means the prompt names the repository or one of its modules,
	// which settles it without scoring, and Confidence is 1.
	Named bool `json:"named,omitempty"`
	// Reasons say what matched where, for a person to read, best first,
	// such as "pricing in internal/pricing/".
	Reasons []string `json:"reasons,omitempty"`
}

// PickRequest asks an island's agent which of the island's repositories
// that scored close a prompt is for. The agent is given only the
// repositories' short descriptions, never another island's.
type PickRequest struct {
	Prompt string `json:"prompt"`
	// Among names the repositories to choose between, by id: at least two,
	// all on this island.
	Among []string `json:"among"`
	// Agent is the agent to ask, when the island can run it. Otherwise
	// the daemon asks one it can.
	Agent string `json:"agent,omitempty"`
	// SessionID is the session the browser starts once the repository is
	// chosen. What the question cost is recorded on that session when it
	// starts, as a TokensUsed for TokensForDispatch.
	SessionID string `json:"sessionId,omitempty"`
}

// PickResult is what the agent chose: a repository, or none when it
// couldn't tell, and the agent that was asked.
type PickResult struct {
	RepoID string `json:"repoId,omitempty"`
	Agent  string `json:"agent"`
}
