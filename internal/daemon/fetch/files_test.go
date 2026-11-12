package fetch

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestMatchFilesPutsTheBestFirst(t *testing.T) {
	dir := t.TempDir()
	files := []string{"README.md", "cmd/main.go", "internal/main_test.go", "internal/domain/main.go", "web/src/maintenance.ts", "docs/m-a-i-n.txt", "gone.go"}
	for _, f := range files {
		if f == "gone.go" {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := matchFiles(dir, files, "main", 10)
	want := []string{"cmd/main.go", "internal/domain/main.go", "internal/main_test.go", "web/src/maintenance.ts", "docs/m-a-i-n.txt"}
	if !slices.Equal(got.Files, want) || got.More {
		t.Errorf("main = %v (more %v), want %v", got.Files, got.More, want)
	}
	// A path can be typed too, and a file listed but gone isn't offered.
	if got := matchFiles(dir, files, "internal/dom", 10); !slices.Equal(got.Files, []string{"internal/domain/main.go"}) {
		t.Errorf("internal/dom = %v", got.Files)
	}
	if got := matchFiles(dir, files, "gone", 10); len(got.Files) != 0 {
		t.Errorf("gone = %v", got.Files)
	}
	// With nothing typed, the shallowest come first, as many as asked.
	if got := matchFiles(dir, files, "", 2); !slices.Equal(got.Files, []string{"README.md", "cmd/main.go"}) || !got.More {
		t.Errorf("nothing typed = %v (more %v)", got.Files, got.More)
	}
}
