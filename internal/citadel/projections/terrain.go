package projections

import (
	"sort"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Repo is one registered repository on one host: a terrace, with a
// building for each of its largest files.
type Repo struct {
	Host   string `json:"host"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Commit string `json:"commit,omitempty"`
	// TotalFiles counts every tracked file. Files holds at most
	// v1.MaxBuildings of them, largest first.
	TotalFiles int              `json:"totalFiles"`
	Files      []v1.TerrainFile `json:"files"`
	// Rules settle tool calls in sessions on this repository; a new session
	// copies them when it starts. Never null, so browsers can index it.
	// RulesBy and RulesApprover are who set them, RulesRevision how many
	// times they were set, which a change names, and RulesLapsed that the
	// approver who signed them no longer approves, so their allow rules
	// ask.
	Rules         []v1.Rule `json:"rules"`
	RulesBy       string    `json:"rulesBy,omitempty"`
	RulesApprover string    `json:"rulesApprover,omitempty"`
	RulesRevision int       `json:"rulesRevision"`
	RulesLapsed   bool      `json:"rulesLapsed,omitempty"`
	RegisteredAt  time.Time `json:"registeredAt"`
	SnapshottedAt time.Time `json:"snapshottedAt"`
}

// Terrain is the projection of every host's repositories, built from host
// events. It is safe for concurrent use.
type Terrain struct {
	mu       sync.Mutex
	byHost   map[string]map[string]*Repo
	onChange func(Repo)
}

// NewTerrain returns an empty projection. onChange, if set, gets a copy of
// each repo whose state changed.
func NewTerrain(onChange func(Repo)) *Terrain {
	if onChange == nil {
		onChange = func(Repo) {}
	}
	return &Terrain{byHost: map[string]map[string]*Repo{}, onChange: onChange}
}

// Apply folds one host event into the projection. Events it doesn't know
// are ignored, so new event types never break an older citadel.
func (t *Terrain) Apply(host string, ev v1.Event) {
	var changed *Repo
	t.mu.Lock()
	repos := t.byHost[host]
	if repos == nil {
		repos = map[string]*Repo{}
		t.byHost[host] = repos
	}
	switch ev.Type {
	case v1.EventRepoRegistered:
		var d v1.RepoRegistered
		if ev.Decode(&d) == nil {
			r := &Repo{Host: host, ID: d.RepoID, Name: d.Name, Path: d.Path, Files: []v1.TerrainFile{}, Rules: []v1.Rule{}, RegisteredAt: ev.Time}
			repos[d.RepoID] = r
			changed = r
		}
	case v1.EventRepoRulesSet:
		var d v1.RepoRulesSet
		if ev.Decode(&d) == nil {
			if r, ok := repos[d.RepoID]; ok {
				r.Rules = d.Rules
				if r.Rules == nil {
					r.Rules = []v1.Rule{}
				}
				r.RulesBy, r.RulesApprover, r.RulesLapsed = d.By, d.Approver, false
				r.RulesRevision++
				changed = r
			}
		}
	case v1.EventRepoRulesLapsed:
		var d v1.RepoRulesLapsed
		if ev.Decode(&d) == nil {
			if r, ok := repos[d.RepoID]; ok {
				r.RulesLapsed = true
				changed = r
			}
		}
	case v1.EventTerrainSnapshotted:
		var d v1.TerrainSnapshotted
		if ev.Decode(&d) == nil {
			if r, ok := repos[d.RepoID]; ok {
				r.Commit = d.Commit
				r.TotalFiles = max(d.TotalFiles, len(d.Files))
				r.Files = d.Files
				r.SnapshottedAt = ev.Time
				changed = r
			}
		}
	}
	var copy Repo
	if changed != nil {
		copy = *changed
		copy.Rules = append([]v1.Rule{}, changed.Rules...)
	}
	t.mu.Unlock()
	if changed != nil {
		t.onChange(copy)
	}
}

// List returns every repo, sorted by host then name.
func (t *Terrain) List() []Repo {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Repo
	for _, repos := range t.byHost {
		for _, r := range repos {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Name < out[j].Name
	})
	return out
}
