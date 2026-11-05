package blobs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "blobs"))
	data := []byte(`{"command":"go test ./..."}`)
	hash, err := s.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	if hash != Hash(data) || len(hash) != 64 {
		t.Errorf("Put = %q, want the SHA-256 %q", hash, Hash(data))
	}
	got, err := s.Get(hash)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("Get = %q, %v", got, err)
	}
	// Putting it again keeps the one copy.
	if again, err := s.Put(data); err != nil || again != hash {
		t.Errorf("Put again = %q, %v", again, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "blobs", hash[:2]))
	if len(entries) != 1 {
		t.Errorf("the blob's directory holds %d files, want 1", len(entries))
	}
	// Nothing at all is a blob too.
	empty, err := s.Put(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(empty); err != nil || len(got) != 0 {
		t.Errorf("the empty blob = %q, %v", got, err)
	}

	if _, err := s.Get(Hash([]byte("never kept"))); !errors.Is(err, ErrNotKept) {
		t.Errorf("Get of a blob never kept: %v, want ErrNotKept", err)
	}
	for _, bad := range []string{"", "abc", "../" + hash[3:], hash[:63] + "G", hash[:63] + "A"} {
		if _, err := s.Get(bad); err == nil || errors.Is(err, ErrNotKept) {
			t.Errorf("Get(%q) = %v, want a refusal of the hash", bad, err)
		}
	}

	// A blob changed on disk comes back as it is, for the reader to find
	// it doesn't match.
	if err := os.WriteFile(filepath.Join(dir, "blobs", hash[:2], hash), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(hash); Hash(got) == hash {
		t.Error("a changed blob still matches its hash")
	}
}
