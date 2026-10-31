package v1

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nuid"
)

// Event is the envelope every stored event uses. Type names the payload in
// Data, and CommandID records the command that produced the event, which is
// how an aggregate recognizes a command it has already handled.
type Event struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	CommandID string          `json:"commandId,omitempty"`
	Time      time.Time       `json:"time"`
	Data      json.RawMessage `json:"data"`
}

// NewEvent wraps data as an event of the given type produced by commandID.
func NewEvent(eventType, commandID string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s: %w", eventType, err)
	}
	return Event{Type: eventType, ID: nuid.Next(), CommandID: commandID, Time: time.Now().UTC(), Data: raw}, nil
}

// Decode unmarshals the event's payload into v.
func (e Event) Decode(v any) error {
	if err := json.Unmarshal(e.Data, v); err != nil {
		return fmt.Errorf("decode %s: %w", e.Type, err)
	}
	return nil
}

// Host event types, produced by the Host aggregate on each daemon.
const (
	EventRepoRegistered     = "RepoRegistered"
	EventTerrainSnapshotted = "TerrainSnapshotted"
)

// RepoRegistered records that a repository on the host is now a terrace.
type RepoRegistered struct {
	RepoID string `json:"repoId"`
	Name   string `json:"name"`
	// Path is the repository's top-level directory on the host.
	Path string `json:"path"`
}

// MaxBuildings is the most files a TerrainSnapshotted event carries, and so
// the most buildings a terrace shows: an 8 by 8 grid, which keeps each
// building large enough to click at terrace level. The daemon keeps the
// largest files, until recently touched files can take priority.
const MaxBuildings = 64

// TerrainSnapshotted records a repository's tracked files and their sizes,
// which become the buildings on its terrace. Files holds at most
// MaxBuildings entries, largest first, and TotalFiles counts them all.
type TerrainSnapshotted struct {
	RepoID     string        `json:"repoId"`
	Commit     string        `json:"commit,omitempty"`
	TotalFiles int           `json:"totalFiles"`
	Files      []TerrainFile `json:"files"`
}

// TerrainFile is one tracked file.
type TerrainFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// RegisterRepo asks a host to register the repository at Path. ID is chosen
// by the sender, and a repeated ID gets the original outcome.
type RegisterRepo struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}
