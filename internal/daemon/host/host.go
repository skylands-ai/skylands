// Package host is the Host aggregate on a daemon. It handles RegisterRepo
// and the terrain commands, records their events in the host's stream, and
// is rebuilt from that stream at startup. The daemon is the only writer of
// the stream, so this aggregate serializes its commands.
package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/terrain"
	"github.com/skylands-ai/skylands/internal/es"
)

// Repo is a registered repository: a terrace on the island.
type Repo struct {
	ID            string
	Name          string
	Path          string
	Commit        string
	TotalFiles    int
	Files         []v1.TerrainFile
	RegisteredAt  time.Time
	SnapshottedAt time.Time
}

// ErrAlreadyRegistered means the repository is already a terrace.
var ErrAlreadyRegistered = errors.New("repository is already registered")

// Host is the aggregate for one island.
type Host struct {
	name  string
	store *es.Store

	// cmdMu serializes command handling, so checks and appends don't race.
	cmdMu sync.Mutex
	mu    sync.Mutex
	repos map[string]*Repo
	// byPath finds a repo by its top-level path.
	byPath map[string]string
	// handled maps a command id to the repo it produced, for idempotency.
	handled map[string]string
}

// Load rebuilds the aggregate from the host's stream.
func Load(ctx context.Context, store *es.Store, name string) (*Host, error) {
	h := &Host{name: name, store: store, repos: map[string]*Repo{}, byPath: map[string]string{}, handled: map[string]string{}}
	err := store.Replay(ctx, v1.IslandStreamName(name), []string{v1.IslandEventFilter(name)}, func(seq uint64, ev v1.Event) error {
		h.apply(ev)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load host %s: %w", name, err)
	}
	return h, nil
}

// Repo returns one registered repository by id.
func (h *Host) Repo(id string) (Repo, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.repos[id]
	if !ok {
		return Repo{}, false
	}
	return *r, true
}

// Repos lists the registered repositories by name.
func (h *Host) Repos() []Repo {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Repo, 0, len(h.repos))
	for _, r := range h.repos {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RegisterRepo handles the command: it validates the path, snapshots the
// repository, and records RepoRegistered and TerrainSnapshotted. A command
// id already in the history gets its original repo back and produces no new
// events.
func (h *Host) RegisterRepo(ctx context.Context, cmd v1.RegisterRepo) (Repo, error) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()

	h.mu.Lock()
	if id, ok := h.handled[cmd.ID]; ok {
		r := *h.repos[id]
		h.mu.Unlock()
		return r, nil
	}
	h.mu.Unlock()

	snap, err := terrain.Take(ctx, cmd.Path)
	if err != nil {
		return Repo{}, err
	}
	h.mu.Lock()
	if id, ok := h.byPath[snap.Path]; ok {
		r := *h.repos[id]
		h.mu.Unlock()
		return r, ErrAlreadyRegistered
	}
	h.mu.Unlock()

	id := repoID(h.name, snap.Path)
	registered, err := v1.NewEvent(v1.EventRepoRegistered, cmd.ID, v1.RepoRegistered{RepoID: id, Name: snap.Name, Path: snap.Path})
	if err != nil {
		return Repo{}, err
	}
	snapshotted, err := v1.NewEvent(v1.EventTerrainSnapshotted, cmd.ID, v1.TerrainSnapshotted{RepoID: id, Commit: snap.Commit, TotalFiles: snap.TotalFiles, Files: snap.Files})
	if err != nil {
		return Repo{}, err
	}
	for _, ev := range []v1.Event{registered, snapshotted} {
		if _, err := h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev); err != nil {
			return Repo{}, err
		}
		h.apply(ev)
	}
	slog.Info("host: repo registered", "host", h.name, "repo", snap.Name, "path", snap.Path, "files", snap.TotalFiles)
	h.mu.Lock()
	defer h.mu.Unlock()
	return *h.repos[id], nil
}

// apply folds one event into the state.
func (h *Host) apply(ev v1.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch ev.Type {
	case v1.EventRepoRegistered:
		var d v1.RepoRegistered
		if ev.Decode(&d) != nil {
			return
		}
		h.repos[d.RepoID] = &Repo{ID: d.RepoID, Name: d.Name, Path: d.Path, RegisteredAt: ev.Time}
		h.byPath[d.Path] = d.RepoID
		if ev.CommandID != "" {
			h.handled[ev.CommandID] = d.RepoID
		}
	case v1.EventTerrainSnapshotted:
		var d v1.TerrainSnapshotted
		if ev.Decode(&d) != nil {
			return
		}
		if r, ok := h.repos[d.RepoID]; ok {
			r.Commit = d.Commit
			r.TotalFiles = max(d.TotalFiles, len(d.Files))
			r.Files = d.Files
			r.SnapshottedAt = ev.Time
		}
	}
}

// repoID names a repository stably from its host and path.
func repoID(host, path string) string {
	sum := sha256.Sum256([]byte(host + "\x00" + path))
	return hex.EncodeToString(sum[:6])
}
