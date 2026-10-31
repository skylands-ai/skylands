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
