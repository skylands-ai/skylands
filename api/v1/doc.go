// Package v1 is the public API of a Skylands world: the NATS subject
// namespace, versioned by its first token, and the event and command types
// carried on it. Anything holding credentials scoped to a world's account can
// use this package to send commands and read events directly, without going
// through a citadel.
package v1
