package fetch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// listingFor is how long a directory's listing is used before git is asked
// again, so typing a mention doesn't run git for every key.
const listingFor = 5 * time.Second

// listings keeps each directory's latest listing.
type listings struct {
	mu   sync.Mutex
	byID map[string]listing
}

type listing struct {
	at    time.Time
	files []string
}

// files lists the files in dir that git tracks, and the untracked ones it
// doesn't ignore, as slash-separated paths relative to dir.
func (l *listings) files(ctx context.Context, dir string) ([]string, error) {
	l.mu.Lock()
	if got, ok := l.byID[dir]; ok && time.Since(got.at) < listingFor {
		l.mu.Unlock()
		return got.files, nil
	}
	l.mu.Unlock()
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("the directory is gone: %s", dir)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %s", strings.TrimSpace(stderr.String()))
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	l.mu.Lock()
	if l.byID == nil {
		l.byID = map[string]listing{}
	}
	for d, got := range l.byID {
		if time.Since(got.at) >= listingFor {
			delete(l.byID, d)
		}
	}
	l.byID[dir] = listing{at: time.Now(), files: files}
	l.mu.Unlock()
	return files, nil
}

// matchFiles is the files that best match query, at most limit of them,
// best first, and whether more matched. Without a query, every file
// matches, shallowest first. A file listed that is no longer on disk, as
// one deleted but still in the index, is left out.
func matchFiles(dir string, files []string, query string, limit int) v1.FilesResult {
	q := strings.ToLower(strings.TrimSpace(query))
	type hit struct {
		path  string
		score int
	}
	var hits []hit
	for _, f := range files {
		if score, ok := matchScore(f, q); ok {
			hits = append(hits, hit{f, score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if q == "" {
			if da, db := strings.Count(a.path, "/"), strings.Count(b.path, "/"); da != db {
				return da < db
			}
		} else if len(a.path) != len(b.path) {
			return len(a.path) < len(b.path)
		}
		return a.path < b.path
	})
	out := v1.FilesResult{Files: []string{}}
	for i, h := range hits {
		if len(out.Files) == limit {
			out.More = i < len(hits)
			break
		}
		if fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(h.path))); err != nil || fi.IsDir() {
			continue
		}
		out.Files = append(out.Files, h.path)
	}
	return out
}

// matchScore says how well a path matches what was typed, lower being
// better: the file's name first, then its whole path, then the typed
// letters in order anywhere in it.
func matchScore(file, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	p := strings.ToLower(file)
	base := path.Base(p)
	switch {
	case base == q || strings.TrimSuffix(base, path.Ext(base)) == q:
		return 0, true
	case strings.HasPrefix(base, q):
		return 1, true
	case strings.Contains(base, q):
		return 2, true
	case strings.HasPrefix(p, q):
		return 3, true
	case strings.Contains(p, q):
		return 4, true
	case inOrder(p, q):
		return 5, true
	}
	return 0, false
}

// inOrder reports whether every rune of q appears in s, in order.
func inOrder(s, q string) bool {
	for _, r := range q {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}
