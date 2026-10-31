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
