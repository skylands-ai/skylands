// Package blobs keeps bytes on the host under their SHA-256: the exact
// input of every request that waited on a person or a rule, the whole
// output of every finished tool call, and the files and images a prompt
// gave the agent. Events name them by hash, so each can be checked
// against a signed decision or a session's record long after the agent
// moved on, whatever its size. Nothing in the store is removed. Images
// uploaded for a prompt wait apart, in Uploads, until the prompt is sent.
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNotKept means nothing is kept under a hash.
var ErrNotKept = errors.New("not kept on this host")

// Store keeps blobs in a directory, one file per blob, named by its hash
// under a directory named by the hash's first two characters.
type Store struct {
	dir string
}

// New returns a store in dir, which is made when the first blob is kept.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Hash is the SHA-256 of data in lowercase hex, which is what the store
// names it by.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Put keeps data and returns its hash. Data already kept is left as it
// is. A blob is written to a temporary file and renamed into place, so a
// reader never sees part of one.
func (s *Store) Put(data []byte) (string, error) {
	hash := Hash(data)
	path, err := s.path(hash)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("blobs: %w", err)
	}
	tmp, err := os.CreateTemp(dir, hash+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("blobs: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("blobs: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("blobs: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("blobs: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("blobs: %w", err)
	}
	return hash, nil
}

// Get returns the bytes kept under hash, as they are on disk. It doesn't
// check them against the hash: whoever relies on them does, so a blob
// changed on disk shows as one that doesn't match.
func (s *Store) Get(hash string) ([]byte, error) {
	path, err := s.path(hash)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotKept
	}
	if err != nil {
		return nil, fmt.Errorf("blobs: %w", err)
	}
	return data, nil
}

// path is where a blob lives, for a hash that is 64 lowercase hex
// characters and nothing else.
func (s *Store) path(hash string) (string, error) {
	if len(hash) != sha256.Size*2 {
		return "", fmt.Errorf("blobs: %q isn't a SHA-256", hash)
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("blobs: %q isn't a SHA-256 in lowercase hex", hash)
		}
	}
	return filepath.Join(s.dir, hash[:2], hash), nil
}
