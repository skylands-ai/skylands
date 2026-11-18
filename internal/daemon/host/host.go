// Package host is the Host aggregate on a daemon. It handles RegisterRepo,
// AddRepo, and the terrain commands, records their events in the host's
// stream, and is rebuilt from that stream at startup. The daemon is the
// only writer of the stream, so this aggregate serializes its commands.
package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"sort"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/approval"
	"github.com/skylands-ai/skylands/internal/daemon/clone"
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
	// URL is what the daemon cloned the repository from, and empty for one
	// that was on the host already. RegisteredBy and RegisteredApprover
	// are who added it, as RepoRegistered records it.
	URL                string
	RegisteredBy       string
	RegisteredApprover string
	RegisteredAt       time.Time
	SnapshottedAt      time.Time
	// Remotes are its git remotes as last recorded, by name.
	Remotes []v1.RepoRemote
}

// Clone is a clone a browser asked for, as the host's stream records it.
type Clone struct {
	// ID is the id of the AddRepo that asked for it.
	ID       string
	URL      string
	Path     string
	Approver string
	Started  time.Time
	// Error is why the clone failed, and Registered that it is a terrace
	// now. Neither is set while git is cloning.
	Error      string
	Registered bool
}

// Cloner clones url into dir by way of partial, as clone.Into does.
type Cloner func(ctx context.Context, url, dir, partial string) error

var (
	// ErrAlreadyRegistered means the repository is already a terrace.
	ErrAlreadyRegistered = errors.New("repository is already registered")
	ErrUnknownRepo       = errors.New("no such repository on this host")
)

// addIDPattern is the id a browser picks for an AddRepo, which names the
// directory a clone works in until it is done.
var addIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

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

	// clones are every clone the stream records, by the id of the AddRepo
	// that asked for it.
	clones map[string]*Clone
	// root, cloneCtx, and cloner are as AllowAdding sets them, and cloning
	// counts the clones running.
	root     string
	cloneCtx context.Context
	cloner   Cloner
	cloning  sync.WaitGroup
}

// AllowAdding lets browsers add repositories to the host: root is the
// directory each one must be inside, the clone root, and clones run in the
// background under ctx, each by clone.
func (h *Host) AllowAdding(ctx context.Context, root string, clone Cloner) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.root, h.cloneCtx, h.cloner = root, ctx, clone
}

// Root is the clone root, or "" before AllowAdding.
func (h *Host) Root() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.root
}

// RequireSignatures makes a change to a repository's rules, through the
// citadel or the host's CLI, need an approver's signature once the world
// exists: world is
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
	h := &Host{name: name, store: store, repos: map[string]*Repo{}, byPath: map[string]string{}, handled: map[string]string{}, clones: map[string]*Clone{}}
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

// RegisterRepo handles the command from the host's CLI: it validates the
// path, snapshots the repository, and records RepoRegistered, by "local",
// and TerrainSnapshotted. A command id already in the history gets its
// original repo back and produces no new events.
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
	return h.register(ctx, cmd.ID, snap, v1.RepoRegistered{By: "local"})
}

// register records RepoRegistered, with who added the repository as added
// names them, and TerrainSnapshotted for snap. The caller holds cmdMu.
func (h *Host) register(ctx context.Context, commandID string, snap terrain.Snapshot, added v1.RepoRegistered) (Repo, error) {
	h.mu.Lock()
	if id, ok := h.byPath[snap.Path]; ok {
		r := *h.repos[id]
		h.mu.Unlock()
		return r, ErrAlreadyRegistered
	}
	h.mu.Unlock()

	id := repoID(h.name, snap.Path)
	added.RepoID, added.Name, added.Path = id, snap.Name, snap.Path
	registered, err := v1.NewEvent(v1.EventRepoRegistered, commandID, added)
	if err != nil {
		return Repo{}, err
	}
	snapshotted, err := v1.NewEvent(v1.EventTerrainSnapshotted, commandID, v1.TerrainSnapshotted{RepoID: id, Commit: snap.Commit, TotalFiles: snap.TotalFiles, Files: snap.Files})
	if err != nil {
		return Repo{}, err
	}
	for _, ev := range []v1.Event{registered, snapshotted} {
		if _, err := h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev); err != nil {
			return Repo{}, err
		}
		h.apply(ev)
	}
	slog.Info("host: repo registered", "host", h.name, "repo", snap.Name, "path", snap.Path, "files", snap.TotalFiles, "by", added.By, "url", added.URL)
	if remotes, err := terrain.Remotes(ctx, snap.Path); err != nil {
		slog.Warn("host: read a repository's remotes", "repo", snap.Name, "err", err)
	} else if err := h.recordRemotes(ctx, id, remotes); err != nil {
		slog.Warn("host: record a repository's remotes", "repo", snap.Name, "err", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return *h.repos[id], nil
}

// CheckRemotes reads every repository's git remotes and records those
// that changed since they were last recorded, as RepoRemotesChanged. The
// daemon checks when it starts and then now and then, so a repository
// registered before remotes were recorded, or whose remotes changed,
// matches issue links by the remotes it has.
func (h *Host) CheckRemotes(ctx context.Context) {
	for _, r := range h.Repos() {
		remotes, err := terrain.Remotes(ctx, r.Path)
		if err != nil {
			slog.Debug("host: read a repository's remotes", "repo", r.Name, "err", err)
			continue
		}
		h.cmdMu.Lock()
		err = h.recordRemotes(ctx, r.ID, remotes)
		h.cmdMu.Unlock()
		if err != nil {
			slog.Warn("host: record a repository's remotes", "repo", r.Name, "err", err)
		}
	}
}

// recordRemotes records RepoRemotesChanged for the repository when
// remotes differ from what was last recorded. The caller holds cmdMu.
func (h *Host) recordRemotes(ctx context.Context, repoID string, remotes []v1.RepoRemote) error {
	h.mu.Lock()
	r, ok := h.repos[repoID]
	same := ok && (len(r.Remotes) == 0 && len(remotes) == 0 || reflect.DeepEqual(r.Remotes, remotes))
	h.mu.Unlock()
	if !ok || same {
		return nil
	}
	ev, err := v1.NewEvent(v1.EventRepoRemotesChanged, "", v1.RepoRemotesChanged{RepoID: repoID, Remotes: remotes})
	if err != nil {
		return err
	}
	if _, err := h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev); err != nil {
		return err
	}
	h.apply(ev)
	return nil
}

// AddRepo handles AddRepo from a browser through the citadel. It must
// carry an approver's signature over it, and its path must be inside the
// clone root. A path is registered as RegisterRepo registers one, by
// "citadel", once the repository's top level is found inside the root too.
// A URL is cloned in the background: AddRepo records RepoCloneStarted and
// returns, and the clone ends in RepoRegistered or RepoCloneFailed. A
// command id already in the history gets its original outcome back and
// produces no new events.
func (h *Host) AddRepo(ctx context.Context, cmd v1.AddRepo) (v1.AddRepoResult, error) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	if out, err, ok := h.added(cmd.ID); ok {
		return out, err
	}
	if !addIDPattern.MatchString(cmd.ID) {
		return v1.AddRepoResult{}, errors.New("an addition's id is 8 to 64 letters and digits")
	}
	h.mu.Lock()
	root, cloneCtx, cloner := h.root, h.cloneCtx, h.cloner
	h.mu.Unlock()
	if root == "" || cloner == nil {
		return v1.AddRepoResult{}, errors.New("this daemon doesn't add repositories from a browser")
	}
	world, verify := h.signer()
	if world == "" {
		return v1.AddRepoResult{}, errors.New("this island's world isn't set up yet")
	}
	if cmd.Signature == nil {
		return v1.AddRepoResult{}, approval.ErrUnsigned
	}
	d := approval.RepoAdd{V: approval.DecisionVersion, Kind: approval.KindRepoAdd, World: world, Host: h.name, ID: cmd.ID, Path: cmd.Path, URL: cmd.URL}
	if err := verify(ctx, d, *cmd.Signature); err != nil {
		slog.Warn("host: an addition's signature was refused", "path", cmd.Path, "url", cmd.URL, "approver", cmd.Signature.Approver, "err", err)
		return v1.AddRepoResult{}, err
	}
	added := v1.RepoRegistered{By: "citadel", Approver: cmd.Signature.Approver, Signature: cmd.Signature.Sig}

	if cmd.URL == "" {
		path, err := clone.Within(root, cmd.Path)
		if err != nil {
			return v1.AddRepoResult{}, err
		}
		snap, err := terrain.Take(ctx, path)
		if err != nil {
			return v1.AddRepoResult{}, err
		}
		// A directory inside the root may belong to a repository that
		// isn't: agents would work in all of it.
		if realRoot, err := clone.Within(root, root); err != nil || !clone.Inside(realRoot, snap.Path) {
			return v1.AddRepoResult{}, fmt.Errorf("%s is in the repository at %s, which is %w", path, snap.Path, clone.ErrOutside)
		}
		r, err := h.register(ctx, cmd.ID, snap, added)
		if err != nil {
			return v1.AddRepoResult{}, err
		}
		return v1.AddRepoResult{RepoID: r.ID, Name: r.Name, Path: r.Path}, nil
	}

	if err := clone.CheckURL(cmd.URL); err != nil {
		return v1.AddRepoResult{}, err
	}
	dir, err := clone.Within(root, cmd.Path)
	if err != nil {
		return v1.AddRepoResult{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return v1.AddRepoResult{}, fmt.Errorf("%s already exists", dir)
	}
	h.mu.Lock()
	for _, c := range h.clones {
		if c.Path == dir && c.Error == "" && !c.Registered {
			h.mu.Unlock()
			return v1.AddRepoResult{}, fmt.Errorf("a clone into %s is already running", dir)
		}
	}
	h.mu.Unlock()
	ev, err := v1.NewEvent(v1.EventRepoCloneStarted, cmd.ID, v1.RepoCloneStarted{URL: cmd.URL, Path: dir, By: added.By, Approver: added.Approver, Signature: added.Signature})
	if err != nil {
		return v1.AddRepoResult{}, err
	}
	if _, err := h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev); err != nil {
		return v1.AddRepoResult{}, err
	}
	h.apply(ev)
	slog.Info("host: cloning", "host", h.name, "url", cmd.URL, "path", dir)
	h.cloning.Add(1)
	go h.runClone(cloneCtx, cmd.ID, cmd.URL, dir, added, cloner)
	return v1.AddRepoResult{Path: dir, Cloning: true}, nil
}

// added is the outcome of the AddRepo with this id, if the history has it.
func (h *Host) added(id string) (v1.AddRepoResult, error, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == "" {
		return v1.AddRepoResult{}, nil, false
	}
	if repoID, ok := h.handled[id]; ok {
		r := h.repos[repoID]
		return v1.AddRepoResult{RepoID: r.ID, Name: r.Name, Path: r.Path}, nil, true
	}
	if c, ok := h.clones[id]; ok {
		if c.Error != "" {
			return v1.AddRepoResult{Path: c.Path}, errors.New(c.Error), true
		}
		return v1.AddRepoResult{Path: c.Path, Cloning: true}, nil, true
	}
	return v1.AddRepoResult{}, nil, false
}

// runClone clones url into dir and registers the clone, or records why it
// failed. A clone cut off because the daemon is stopping is left for
// FailInterruptedClones when it starts again.
func (h *Host) runClone(ctx context.Context, id, url, dir string, added v1.RepoRegistered, cloner Cloner) {
	defer h.cloning.Done()
	err := cloner(ctx, url, dir, clone.Partial(dir, id))
	if ctx.Err() != nil {
		return
	}
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	if err == nil {
		var snap terrain.Snapshot
		if snap, err = terrain.Take(ctx, dir); err == nil {
			added.URL = url
			if _, err = h.register(ctx, id, snap, added); err == nil {
				return
			}
		}
		// The clone is the daemon's own, made a moment ago where nothing
		// was, so nothing is lost removing it.
		_ = os.RemoveAll(dir)
	}
	slog.Warn("host: a clone failed", "host", h.name, "url", url, "path", dir, "err", err)
	h.failClone(ctx, id, url, dir, err.Error())
}

// failClone records RepoCloneFailed. The caller holds cmdMu.
func (h *Host) failClone(ctx context.Context, id, url, dir, why string) {
	ev, err := v1.NewEvent(v1.EventRepoCloneFailed, id, v1.RepoCloneFailed{URL: url, Path: dir, Error: why})
	if err == nil {
		_, err = h.store.Append(ctx, v1.IslandEventSubject(h.name, ev.Type), ev)
	}
	if err != nil {
		slog.Warn("host: record a failed clone", "url", url, "err", err)
		return
	}
	h.apply(ev)
}

// FailInterruptedClones records RepoCloneFailed for each clone the stream
// says started and never ended, as a daemon that stopped mid-clone leaves
// them, and removes what each had fetched. The daemon calls it as it
// starts, before it takes commands.
func (h *Host) FailInterruptedClones(ctx context.Context) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	h.mu.Lock()
	var open []Clone
	for _, c := range h.clones {
		if c.Error == "" && !c.Registered {
			open = append(open, *c)
		}
	}
	h.mu.Unlock()
	for _, c := range open {
		_ = os.RemoveAll(clone.Partial(c.Path, c.ID))
		h.failClone(ctx, c.ID, c.URL, c.Path, "the daemon stopped before the clone was done")
	}
}

// Cloning reports whether a clone is running.
func (h *Host) Cloning() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clones {
		if c.Error == "" && !c.Registered {
			return true
		}
	}
	return false
}

// Clones lists every clone the stream records, oldest first.
func (h *Host) Clones() []Clone {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Clone, 0, len(h.clones))
	for _, c := range h.clones {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// Wait waits for the clones running to end, once the context AllowAdding
// was given is done.
func (h *Host) Wait() { h.cloning.Wait() }

// SetRules handles SetRepoRules: the repository's rules become the given
// list, which sessions started on it from now on copy. A command id
// already in the history gets the repo back and changes nothing. By is
// "local" for the host's CLI or "citadel". Either way the change names the
// revision of the rules it replaces, and once the world exists it must be
// signed by one of the world's approvers, which a host's CLI can be.
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
	list, err := rules.ForRepository(cmd.Rules)
	if err != nil {
		return *r, err
	}
	if cmd.Revision != revision {
		return *r, fmt.Errorf("%w: the repository's rules are at revision %d, not %d", approval.ErrStale, revision, cmd.Revision)
	}
	var approver, signature string
	if world, verify := h.signer(); world != "" {
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
// lapse the same way, since a host's CLI signs them as an approver, and
// nothing lapses before the world exists.
func (h *Host) LapseRules(ctx context.Context, valid func(approver string) bool) {
	if world, _ := h.signer(); world == "" {
		return
	}
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	for _, r := range h.Repos() {
		if r.RulesLapsed || !rules.HasAllow(r.Rules) || valid(r.RulesApprover) {
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
		h.repos[d.RepoID] = &Repo{ID: d.RepoID, Name: d.Name, Path: d.Path, URL: d.URL, RegisteredBy: d.By, RegisteredApprover: d.Approver, RegisteredAt: ev.Time}
		h.byPath[d.Path] = d.RepoID
		if ev.CommandID != "" {
			h.handled[ev.CommandID] = d.RepoID
			if c, ok := h.clones[ev.CommandID]; ok {
				c.Registered = true
			}
		}
	case v1.EventRepoRemotesChanged:
		var d v1.RepoRemotesChanged
		if ev.Decode(&d) != nil {
			return
		}
		if r, ok := h.repos[d.RepoID]; ok {
			r.Remotes = d.Remotes
		}
	case v1.EventRepoCloneStarted:
		var d v1.RepoCloneStarted
		if ev.Decode(&d) != nil || ev.CommandID == "" {
			return
		}
		h.clones[ev.CommandID] = &Clone{ID: ev.CommandID, URL: d.URL, Path: d.Path, Approver: d.Approver, Started: ev.Time}
	case v1.EventRepoCloneFailed:
		var d v1.RepoCloneFailed
		if ev.Decode(&d) != nil {
			return
		}
		if c, ok := h.clones[ev.CommandID]; ok {
			c.Error = d.Error
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
