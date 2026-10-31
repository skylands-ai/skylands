// Package socket is the daemon's local API for the CLI: HTTP over a Unix
// socket that only the OS user owning the daemon can open. The daemon
// serves it, and the CLI dials it.
package socket

import "time"

// RegisterRepoRequest is the body of POST /v1/repos.
type RegisterRepoRequest struct {
	Path string `json:"path"`
	// CommandID identifies the command. A repeated id gets the original
	// outcome. The client fills it in when empty.
	CommandID string `json:"commandId,omitempty"`
}

// RepoInfo describes one registered repository.
type RepoInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Commit string `json:"commit,omitempty"`
	// Files counts every tracked file, not only the ones that get buildings.
	Files        int       `json:"files"`
	RegisteredAt time.Time `json:"registeredAt"`
}

// SessionInfo describes one agent session on this host.
type SessionInfo struct {
	ID        string    `json:"id"`
	Repo      string    `json:"repo"`
	Agent     string    `json:"agent"`
	Status    string    `json:"status"`
	Turns     int       `json:"turns"`
	Title     string    `json:"title,omitempty"`
	Prompt    string    `json:"prompt"`
	StartedAt time.Time `json:"startedAt"`
}

// StatusInfo is what GET /v1/status returns.
type StatusInfo struct {
	Host     string        `json:"host"`
	World    string        `json:"world"`
	Version  string        `json:"version"`
	Started  time.Time     `json:"started"`
	Repos    []RepoInfo    `json:"repos"`
	Sessions []SessionInfo `json:"sessions,omitempty"`
}

// ErrorResponse is the body of every error reply.
type ErrorResponse struct {
	Error string `json:"error"`
}
