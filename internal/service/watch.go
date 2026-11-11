package service

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"time"
)

// Change is what became of the binary a service was started from.
type Change int

const (
	// Replaced means the path names another binary, as it does after a
	// package manager upgrades skylands.
	Replaced Change = iota + 1
	// Removed means the path has named nothing for the whole grace
	// period, as after a package manager uninstalls skylands.
	Removed
)

func (c Change) String() string {
	switch c {
	case Replaced:
		return "replaced"
	case Removed:
		return "removed"
	}
	return "unchanged"
}

// WatchBinary checks the file at path every interval, following links,
// and returns once it has been replaced or removed. A new file counts once
// it has stayed the same for a whole interval, so a binary still being
// written, or one Homebrew has yet to finish installing, is not run early.
// A missing file counts once it has been missing for grace: Homebrew
// removes the old link before it adds the new one, so an upgrade leaves
// the path briefly empty. It returns ctx's error if ctx ends first, and an
// error if path names nothing to begin with.
func WatchBinary(ctx context.Context, path string, interval, grace time.Duration) (Change, error) {
	start, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	var (
		candidate   fs.FileInfo // a new file, waiting to stay the same
		missingFrom time.Time   // when the path was first found empty
	)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-t.C:
		}
		now, err := os.Stat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			candidate = nil
			if missingFrom.IsZero() {
				missingFrom = time.Now()
			}
			if time.Since(missingFrom) >= grace {
				return Removed, nil
			}
			continue
		case err != nil:
			// Unreadable for now, such as on a volume being remounted:
			// neither replaced nor removed.
			continue
		}
		missingFrom = time.Time{}
		switch {
		case sameBinary(start, now):
			candidate = nil
		case candidate != nil && sameBinary(candidate, now):
			return Replaced, nil
		default:
			candidate = now
		}
	}
}

// sameBinary reports whether a and b describe the same file with the same
// contents, as far as its size and modification time tell: a binary
// copied over the old one in place keeps the old one's inode.
func sameBinary(a, b fs.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
