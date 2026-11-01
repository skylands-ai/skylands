package v1

// The subject namespace is the public API, versioned by its first token.
// Events are captured by JetStream streams. Commands and fetches are
// request-reply that no stream captures.

// IslandStreamName is the stream that captures one host's events, in the
// host's own JetStream domain and, mirrored, in the world's.
func IslandStreamName(host string) string { return "island-" + host }

// IslandSubjects are the subjects the host's stream captures: host events
// and session events.
func IslandSubjects(host string) []string {
	return []string{IslandEventFilter(host), "v1.island." + host + ".session.*.evt.>"}
}

// IslandEventFilter matches every host event for host, such as terrain
// snapshots and adapter availability.
func IslandEventFilter(host string) string { return "v1.island." + host + ".evt.>" }

// IslandEventSubject is where one host event of the given type is published.
func IslandEventSubject(host, eventType string) string {
	return "v1.island." + host + ".evt." + eventType
}

// SessionEventSubject is where one session event of the given type is
// published.
func SessionEventSubject(host, session, eventType string) string {
	return "v1.island." + host + ".session." + session + ".evt." + eventType
}

// SessionCommandSubject is where one command for a session is sent, by
// request-reply.
func SessionCommandSubject(host, session, commandType string) string {
	return "v1.island." + host + ".session." + session + ".cmd." + commandType
}

// RepoCommandSubject is where one command for a repository on host, such
// as SetRepoRules, is sent by request-reply.
func RepoCommandSubject(host, repo, commandType string) string {
	return "v1.island." + host + ".repo." + repo + ".cmd." + commandType
}

// RepoCommandFilter matches every repository command for host.
func RepoCommandFilter(host string) string { return "v1.island." + host + ".repo.*.cmd.>" }

// SessionLiveSubject carries a session's transcript as it is written: plain
// NATS, no stream, for whoever is reading the conversation right now.
func SessionLiveSubject(host, session string) string {
	return "v1.island." + host + ".session." + session + ".live"
}

// SessionCommandFilter matches every session command for host.
func SessionCommandFilter(host string) string { return "v1.island." + host + ".session.*.cmd.>" }

// SessionEventFilter matches every session event for host.
func SessionEventFilter(host string) string { return "v1.island." + host + ".session.*.evt.>" }

// IslandFetchSubject is where an on-demand read of one kind, such as a
// session's transcript or diff, is requested from host's daemon. The daemon
// answers from the host's disk, so nothing leaves the host until asked.
func IslandFetchSubject(host, kind string) string { return "v1.island." + host + ".fetch." + kind }

// IslandFetchFilter matches every fetch request for host.
func IslandFetchFilter(host string) string { return "v1.island." + host + ".fetch.>" }
