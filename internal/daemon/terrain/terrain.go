// Package terrain turns a repository on the host into the files the world
// shows as buildings: it lists the tracked files and their sizes. A file
// watcher that attributes changes to sessions comes later.
package terrain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// Snapshot is a repository's tracked files at one moment.
type Snapshot struct {
	// Name is the repository directory's name.
	Name string
	// Path is the repository's top-level directory.
	Path   string
	Commit string
	// TotalFiles counts every tracked file.
	TotalFiles int
	// Files holds the largest v1.MaxBuildings of them, largest first.
	Files []v1.TerrainFile
}

// ErrNotRepository means the path is not inside a git working tree.
var ErrNotRepository = errors.New("not inside a git repository")

// Take snapshots the repository containing path. Any path inside the
// working tree resolves to its top level.
func Take(ctx context.Context, path string) (Snapshot, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := os.Stat(abs); err != nil {
		return Snapshot{}, err
	}
	top, err := git(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s: %w", path, ErrNotRepository)
	}
	top = strings.TrimSpace(top)

	list, err := git(ctx, top, "ls-files", "-z")
	if err != nil {
		return Snapshot{}, fmt.Errorf("list tracked files in %s: %w", top, err)
	}
	var files []v1.TerrainFile
	for _, name := range strings.Split(strings.TrimSuffix(list, "\x00"), "\x00") {
		if name == "" {
			continue
		}
		info, err := os.Lstat(filepath.Join(top, name))
		if err != nil {
			// Tracked but deleted in the working tree: no building to show.
			continue
		}
		files = append(files, v1.TerrainFile{Path: name, Size: info.Size()})
	}

	total := len(files)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Size != files[j].Size {
			return files[i].Size > files[j].Size
		}
		return files[i].Path < files[j].Path
	})
	if len(files) > v1.MaxBuildings {
		files = files[:v1.MaxBuildings]
	}

	commit, _ := git(ctx, top, "rev-parse", "--verify", "HEAD")
	return Snapshot{Name: filepath.Base(top), Path: top, Commit: strings.TrimSpace(commit), TotalFiles: total, Files: files}, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return string(out), nil
}
