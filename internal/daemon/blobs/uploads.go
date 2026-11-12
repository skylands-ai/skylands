package blobs

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// UploadsExpire is how long an uploaded image waits for a prompt to name
// it before it is removed.
const UploadsExpire = time.Hour

// ErrNotUploaded means no image is uploaded or kept under a hash.
var ErrNotUploaded = errors.New("that image isn't on this host; attach it again")

// Uploads holds the images uploaded for prompts until a prompt names them,
// which moves them into the store, where they stay. An image no prompt
// names within UploadsExpire is removed by Sweep, as is an upload that
// never finished. Each upload arrives in pieces, in order, into a file of
// its own, and once whole it waits under its SHA-256.
type Uploads struct {
	dir   string
	store *Store
	mu    sync.Mutex
}

// NewUploads returns uploads kept in dir until they move into store.
func NewUploads(dir string, store *Store) *Uploads {
	return &Uploads{dir: dir, store: store}
}

// uploadID is an upload's name as the citadel picks it: it names a file,
// so letters and digits only.
var uploadID = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

// Receive takes one piece of an upload. The first piece starts it again
// from nothing; each one after must begin where the last one ended. Once
// the image is whole, its type is checked against its first bytes, and it
// waits under its hash, which the result names.
func (u *Uploads) Receive(p v1.UploadPiece) (v1.UploadResult, error) {
	switch {
	case !uploadID.MatchString(p.Upload):
		return v1.UploadResult{}, errors.New("an upload is named by 8 to 64 letters and digits")
	case !slices.Contains(v1.ImageTypes, p.MimeType):
		return v1.UploadResult{}, fmt.Errorf("an image must be PNG, JPEG, GIF, or WebP, not %q", p.MimeType)
	case p.Size <= 0:
		return v1.UploadResult{}, errors.New("an empty image can't be uploaded")
	case p.Size > v1.MaxImage:
		return v1.UploadResult{}, fmt.Errorf("an image can be at most %d MB", v1.MaxImage/1024/1024)
	case len(p.Data) == 0 || p.Offset < 0 || p.Offset+int64(len(p.Data)) > p.Size:
		return v1.UploadResult{}, errors.New("the piece doesn't fit the image")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := os.MkdirAll(u.dir, 0o700); err != nil {
		return v1.UploadResult{}, fmt.Errorf("uploads: %w", err)
	}
	part := filepath.Join(u.dir, p.Upload+".part")
	flags := os.O_WRONLY | os.O_APPEND
	if p.Offset == 0 {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	} else if fi, err := os.Stat(part); err != nil || fi.Size() != p.Offset {
		return v1.UploadResult{}, errors.New("the upload's pieces came out of order; attach the image again")
	}
	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return v1.UploadResult{}, fmt.Errorf("uploads: %w", err)
	}
	_, werr := f.Write(p.Data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(part)
		return v1.UploadResult{}, fmt.Errorf("uploads: %w", err)
	}
	received := p.Offset + int64(len(p.Data))
	if received < p.Size {
		return v1.UploadResult{Received: received}, nil
	}
	data, err := os.ReadFile(part)
	if err != nil {
		return v1.UploadResult{}, fmt.Errorf("uploads: %w", err)
	}
	if err := checkImage(data, p.MimeType); err != nil {
		_ = os.Remove(part)
		return v1.UploadResult{}, err
	}
	hash := Hash(data)
	if err := os.Rename(part, filepath.Join(u.dir, hash)); err != nil {
		return v1.UploadResult{}, fmt.Errorf("uploads: %w", err)
	}
	return v1.UploadResult{Received: received, Hash: hash}, nil
}

// checkImage checks that data's first bytes say it is an image of type
// mime.
func checkImage(data []byte, mime string) error {
	if got := http.DetectContentType(data); got != mime {
		return fmt.Errorf("the image isn't the %s it says it is", mime)
	}
	return nil
}

// Take returns the image a prompt names: one uploaded and waiting, or one
// the store already keeps, as an earlier prompt may have sent it. It must
// be what img says: its hash, its size, and its type.
func (u *Uploads) Take(img v1.PromptImage) ([]byte, error) {
	if _, err := u.store.path(img.Hash); err != nil {
		return nil, fmt.Errorf("%q isn't the SHA-256 of an image", img.Hash)
	}
	u.mu.Lock()
	data, err := os.ReadFile(filepath.Join(u.dir, img.Hash))
	u.mu.Unlock()
	if errors.Is(err, fs.ErrNotExist) {
		data, err = u.store.Get(img.Hash)
		if errors.Is(err, ErrNotKept) {
			return nil, fmt.Errorf("%w: %s", ErrNotUploaded, img.Name)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("uploads: %w", err)
	}
	switch {
	case Hash(data) != img.Hash:
		return nil, fmt.Errorf("the image kept as %s isn't the one that was uploaded", img.Name)
	case int64(len(data)) != img.Size:
		return nil, fmt.Errorf("%s is %d bytes, not %d", img.Name, len(data), img.Size)
	}
	if err := checkImage(data, img.MimeType); err != nil {
		return nil, fmt.Errorf("%s: %w", img.Name, err)
	}
	return data, nil
}

// Keep moves an image a prompt sent into the store, where it stays.
func (u *Uploads) Keep(data []byte) error {
	hash, err := u.store.Put(data)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := os.Remove(filepath.Join(u.dir, hash)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("uploads: %w", err)
	}
	return nil
}

// Sweep removes every upload, whole or not, last written longer than age
// before now.
func (u *Uploads) Sweep(now time.Time, age time.Duration) (removed int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	entries, err := os.ReadDir(u.dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < age {
			continue
		}
		if os.Remove(filepath.Join(u.dir, e.Name())) == nil {
			removed++
		}
	}
	return removed
}
