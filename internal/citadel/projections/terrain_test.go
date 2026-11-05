package projections

import (
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func TestTerrainFollowsHostEvents(t *testing.T) {
	var changes []Repo
	tr := NewTerrain(func(r Repo) { changes = append(changes, r) })

	reg, _ := v1.NewEvent(v1.EventRepoRegistered, "c1", v1.RepoRegistered{RepoID: "r1", Name: "storefront", Path: "/code/storefront"})
	tr.Apply("ws", reg)
	if len(changes) != 1 || changes[0].Host != "ws" || changes[0].Name != "storefront" || len(changes[0].Files) != 0 {
		t.Fatalf("after register, changes = %+v", changes)
	}

	snap, _ := v1.NewEvent(v1.EventTerrainSnapshotted, "c1", v1.TerrainSnapshotted{RepoID: "r1", Commit: "abc", TotalFiles: 400, Files: []v1.TerrainFile{{Path: "a.go", Size: 10}}})
	tr.Apply("ws", snap)
	if len(changes) != 2 || changes[1].Commit != "abc" || len(changes[1].Files) != 1 || changes[1].TotalFiles != 400 {
		t.Fatalf("after snapshot, changes = %+v", changes)
	}

	// Rules set on the repository reach the terrace, and an empty list
	// still encodes as a list.
	rulesEv, _ := v1.NewEvent(v1.EventRepoRulesSet, "c4", v1.RepoRulesSet{RepoID: "r1", Rules: []v1.Rule{{Decision: "allow", Kind: "edit", Pattern: "worktree"}}})
	tr.Apply("ws", rulesEv)
	if len(changes) != 3 || len(changes[2].Rules) != 1 || changes[2].Rules[0].Pattern != "worktree" {
		t.Fatalf("after rules, changes = %+v", changes)
	}
	lapsed, _ := v1.NewEvent(v1.EventRepoRulesLapsed, "", v1.RepoRulesLapsed{RepoID: "r1", Approver: "UPHONE"})
	tr.Apply("ws", lapsed)
	if r := tr.List()[0]; !r.RulesLapsed || r.RulesRevision != 1 {
		t.Errorf("lapsed rules = lapsed %v, revision %d", r.RulesLapsed, r.RulesRevision)
	}
	cleared, _ := v1.NewEvent(v1.EventRepoRulesSet, "c5", v1.RepoRulesSet{RepoID: "r1", By: "local", Revision: 1})
	tr.Apply("ws", cleared)
	if r := tr.List()[0]; r.Rules == nil || len(r.Rules) != 0 || r.RulesLapsed || r.RulesRevision != 2 || r.RulesBy != "local" {
		t.Errorf("cleared rules = %#v, lapsed %v, revision %d, by %q", r.Rules, r.RulesLapsed, r.RulesRevision, r.RulesBy)
	}
	changes = changes[:2]

	// A snapshot for an unknown repo, and an unknown event type, change nothing.
	orphan, _ := v1.NewEvent(v1.EventTerrainSnapshotted, "c2", v1.TerrainSnapshotted{RepoID: "nope"})
	tr.Apply("ws", orphan)
	other, _ := v1.NewEvent("AdapterAvailabilityChanged", "c3", map[string]bool{"claude": true})
	tr.Apply("ws", other)
	if len(changes) != 2 {
		t.Fatalf("unexpected changes: %+v", changes[2:])
	}

	// A snapshot recorded before totals existed counts the files it lists.
	old, _ := v1.NewEvent(v1.EventTerrainSnapshotted, "c1", map[string]any{"repoId": "r1", "files": []v1.TerrainFile{{Path: "a.go", Size: 10}, {Path: "b.go", Size: 5}}})
	tr.Apply("ws", old)
	if got := tr.List()[0].TotalFiles; got != 2 {
		t.Errorf("TotalFiles without a recorded total = %d, want 2", got)
	}

	reg2, _ := v1.NewEvent(v1.EventRepoRegistered, "c4", v1.RepoRegistered{RepoID: "r2", Name: "infra", Path: "/code/infra"})
	tr.Apply("build", reg2)
	list := tr.List()
	if len(list) != 2 || list[0].Host != "build" || list[1].Host != "ws" {
		t.Fatalf("List = %+v", list)
	}
}
