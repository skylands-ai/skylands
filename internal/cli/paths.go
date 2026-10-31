package cli

import (
	"os"
	"path/filepath"
)

// defaultHome is the directory Skylands keeps its state in on this machine
// when no flag says otherwise.
func defaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".skylands"
	}
	return filepath.Join(home, ".skylands")
}

// defaultDataDir holds the embedded NATS store, agent worktrees, and the
// daemon's other state.
func defaultDataDir() string { return defaultHome() }

// defaultWorldDir is where "world init" writes the owner key and NATS
// settings unless told otherwise.
func defaultWorldDir() string { return filepath.Join(defaultHome(), "world") }

// defaultSocketPath is the Unix socket the daemon listens on and the local
// CLI dials.
func defaultSocketPath() string { return filepath.Join(defaultHome(), "daemon.sock") }
