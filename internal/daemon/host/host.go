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
	"reflect"
	"sort"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/daemon/terrain"
	"github.com/skylands-ai/skylands/internal/es"
	"github.com/skylands-ai/skylands/internal/rules"
)

// Repo is a registered repository: a terrace on the island.
type Repo struct {
	ID         string
	Name       string
	Path       string
	Commit     string
	TotalFiles int
	Files      []v1.TerrainFile
	// Rules settle tool calls in sessions on this repository; a new
	// session copies them when it starts. RulesBy, RulesApprover, and
	// RulesSignature are who set them, as RepoRulesSet records it,
	// RulesRevision counts the times they were set, and RulesLapsed means
	// the approver who signed them no longer approves, so their allow
	// rules ask.
	Rules          []v1.Rule
	RulesBy        string
	RulesApprover  string
	RulesSignature string
	RulesRevision  int
	RulesLapsed    bool
	RegisteredAt   time.Time
	SnapshottedAt  time.Time
}

var (
	// ErrAlreadyRegistered means the repository is already a terrace.
	ErrAlreadyRegistered = errors.New("repository is already registered")
	ErrUnknownRepo       = errors.New("no such repository on this host")
)

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
	// agents is what the host last recorded about the agents it can launch.
	agents []v1.AgentAvailability
	// world and verify check decisions through the citadel, as
	// RequireSignatures sets them.
	world  func() string
	verify func(ctx context.Context, d approval.Decision, s v1.DecisionSignature) error
}

// RequireSignatures makes a change to a repository's rules through the
// citadel need an approver's signature once the world exists: world is
// the world's owner key, or "" before it is known, and verify checks a
// signature against the world's approvers.
func (h *Host) RequireSignatures(world func() string, verify func(ctx context.Context, d approval.Decision, s v1.DecisionSignature) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.world, h.verify = world, verify
}

// signer is the world's owner key, or "" before signatures are needed,
// and the check for them.
func (h *Host) signer() (string, func(ctx context.Context, d approval.Decision, s v1.DecisionSignature) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.world == nil {
		return "", nil
	}
	return h.world(), h.verify
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

// SetRules handles SetRepoRules: the repository's rules become the given
// list, which sessions started on it from now on copy. A command id
// already in the history gets the repo back and changes nothing. By is
// "local" for the host's CLI or "citadel"; a change through the citadel
// names the revision of the rules it replaces, and once the world exists
// it must be signed by one of the world's approvers.
func (h *Host) SetRules(ctx context.Context, cmd v1.SetRepoRules, by string) (Repo, error) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	h.mu.Lock()
	if id, ok := h.handled[cmd.ID]; ok && cmd.ID != "" {
		r := *h.repos[id]
		h.mu.Unlock()
		return r, nil
	}
	r, ok := h.repos[cmd.RepoID]
	if !ok {
		h.mu.Unlock()
		return Repo{}, fmt.Errorf("%w: %s", ErrUnknownRepo, cmd.RepoID)
	}
	revision := r.RulesRevision
	h.mu.Unlock()
	list, err := rules.Validate(cmd.Rules)
	if err != nil {
		return *r, err
	}
	// A repository's rules have no source: they are the source.
	for i := range list {
		list[i].Source = ""
	}
	if by == "citadel" && cmd.Revision != revision {
		return *r, fmt.Errorf("%w: the repository's rules are at revision %d, not %d", approval.ErrStale, revision, cmd.Revision)
	}
	var approver, signature string
	if world, verify := h.signer(); by == "citadel" && world != "" {
		if cmd.Signature == nil {
			return *r, approval.ErrUnsigned
		}
		hash, err := approval.RulesHash(list)
		if err != nil {
			return *r, err
		}
		d := approval.RepoRules{V: approval.DecisionVersion, Kind: approval.KindRepoRules, World: world, Host: h.name, Repo: r.ID, Rules: hash, Revision: revision}
		if err := verify(ctx, d, *cmd.Signature); err != nil {
			slog.Warn("host: a rule change's signature was refused", "repo", r.Name, "approver", cmd.Signature.Approver, "err", err)
			return *r, err
		}
		approver, signature = cmd.Signature.Approver, cmd.Signature.Sig
	}
	ev, err := v1.NewEvent(v1.EventRepoRulesSet, cmd.ID, v1.RepoRulesSet{RepoID: r.ID, Rules: list, By: by, Approver: approver, Signature: signature, Revision: revision})
	if err != nil {
		return Repo{}, err
	}
	if _, err := h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev); err != nil {
		return Repo{}, err
	}
	h.apply(ev)
	slog.Info("host: rules set", "host", h.name, "repo", r.Name, "rules", len(list))
	out, _ := h.Repo(r.ID)
	return out, nil
}

// LapseRules lapses every repository's rules whose signer no longer
// approves, so their allow rules ask until an approver sets them again.
// valid reports whether an approver approves now. Rules set on the host
// never lapse, and nothing does before the world exists.
func (h *Host) LapseRules(ctx context.Context, valid func(approver string) bool) {
	if world, _ := h.signer(); world == "" {
		return
	}
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	for _, r := range h.Repos() {
		if r.RulesLapsed || r.RulesBy == "local" || !rules.HasAllow(r.Rules) || valid(r.RulesApprover) {
			continue
		}
		ev, err := v1.NewEvent(v1.EventRepoRulesLapsed, "", v1.RepoRulesLapsed{RepoID: r.ID, Approver: r.RulesApprover})
		if err != nil {
			continue
		}
		if _, err := h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev); err != nil {
			slog.Warn("host: lapse the rules of a revoked approver", "repo", r.Name, "err", err)
			continue
		}
		h.apply(ev)
		slog.Info("host: rules lapsed, their approver no longer approves", "repo", r.Name, "approver", r.RulesApprover)
	}
}

// Agents is what the host last recorded about the agents it can launch,
// or nil before its first check.
func (h *Host) Agents() []v1.AgentAvailability {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]v1.AgentAvailability(nil), h.agents...)
}

// RecordAgents handles RecordAdapterAvailability: it records
// AdapterAvailabilityChanged when agents differs from what was last
// recorded, and reports whether it did. A daemon checks its agents at every
// start and then periodically, so recording only changes keeps the stream
// from filling with copies.
func (h *Host) RecordAgents(ctx context.Context, agents []v1.AgentAvailability) (bool, error) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	if agents == nil {
		agents = []v1.AgentAvailability{}
	}
	h.mu.Lock()
	same := h.agents != nil && reflect.DeepEqual(h.agents, agents)
	h.mu.Unlock()
	if same {
		return false, nil
	}
	ev, err := v1.NewEvent(v1.EventAdapterAvailabilityChanged, "", v1.AdapterAvailabilityChanged{Agents: agents})
	if err != nil {
		return false, err
	}
	if _, err := h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev); err != nil {
		return false, err
	}
	h.apply(ev)
	return true, nil
}

// apply folds one event into the state.
func (h *Host) apply(ev v1.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch ev.Type {
	case v1.EventAdapterAvailabilityChanged:
		var d v1.AdapterAvailabilityChanged
		if ev.Decode(&d) != nil {
			return
		}
		h.agents = d.Agents
		if h.agents == nil {
			h.agents = []v1.AgentAvailability{}
		}
	case v1.EventRepoRulesSet:
		var d v1.RepoRulesSet
		if ev.Decode(&d) != nil {
			return
		}
		if r, ok := h.repos[d.RepoID]; ok {
			r.Rules, r.RulesBy, r.RulesApprover, r.RulesSignature, r.RulesLapsed = d.Rules, d.By, d.Approver, d.Signature, false
			r.RulesRevision++
			if ev.CommandID != "" {
				h.handled[ev.CommandID] = d.RepoID
			}
		}
	case v1.EventRepoRulesLapsed:
		var d v1.RepoRulesLapsed
		if ev.Decode(&d) != nil {
			return
		}
		if r, ok := h.repos[d.RepoID]; ok {
			r.RulesLapsed = true
		}
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
