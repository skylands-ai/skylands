package v1

import "time"

// RepoRemote is one of a repository's git remotes. URL is as git has it,
// without a password, or for https any user name, which can be a token.
// Host and Path name the repository it points to, such as "github.com"
// and "org/storefront", lowercase and without a trailing .git, for issue
// links to match; a remote that names no host, such as a path on disk,
// has neither.
type RepoRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
}

// EventRepoRemotesChanged records a repository's git remotes, when it is
// registered and whenever the daemon finds them changed since.
const EventRepoRemotesChanged = "RepoRemotesChanged"

// RepoRemotesChanged carries every remote the repository has, by name.
type RepoRemotesChanged struct {
	RepoID  string       `json:"repoId"`
	Remotes []RepoRemote `json:"remotes"`
}

// FetchIssue reads an issue, through the island's gh, for a person to
// read in a prompt before they sign it. The island reads only an issue in
// a repository one of the registered repository's remotes points to.
const FetchIssue = "issue"

// IssueTimeout is how long a citadel waits for an island to read an
// issue.
const IssueTimeout = 20 * time.Second

// IssueRequest asks for the issue at URL, in the repository RepoID.
type IssueRequest struct {
	RepoID string `json:"repoId"`
	URL    string `json:"url"`
}

// Issue is an issue as gh read it.
type Issue struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	// State is "OPEN" or "CLOSED".
	State string `json:"state"`
}
