package blobs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// png is a PNG's header followed by n bytes, which is all the type check
// looks at.
func png(n int) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte{7}, n)...)
}

func TestUploadsArriveInPiecesAndMoveIntoTheStore(t *testing.T) {
	dir := t.TempDir()
	store := New(filepath.Join(dir, "blobs"))
	u := NewUploads(filepath.Join(dir, "uploads"), store)
	img := png(3000)
	piece := func(off, end int) v1.UploadPiece {
		return v1.UploadPiece{Upload: "Upload0001", Offset: int64(off), Data: img[off:end], Size: int64(len(img)), MimeType: "image/png"}
	}
	got, err := u.Receive(piece(0, 1000))
	if err != nil || got.Received != 1000 || got.Hash != "" {
		t.Fatalf("first piece = %+v, %v", got, err)
	}
	if _, err := u.Receive(piece(2000, 3008)); err == nil {
		t.Error("a piece out of order was taken")
	}
	if got, err = u.Receive(piece(1000, 2000)); err != nil || got.Received != 2000 {
		t.Fatalf("second piece = %+v, %v", got, err)
	}
	if got, err = u.Receive(piece(2000, 3008)); err != nil || got.Hash != Hash(img) || got.Received != int64(len(img)) {
		t.Fatalf("last piece = %+v, %v; want hash %s", got, err, Hash(img))
	}

	want := v1.PromptImage{Name: "a.png", MimeType: "image/png", Hash: got.Hash, Size: int64(len(img))}
	for name, bad := range map[string]v1.PromptImage{
		"another size": {Name: "a.png", MimeType: "image/png", Hash: got.Hash, Size: 10},
		"another type": {Name: "a.png", MimeType: "image/gif", Hash: got.Hash, Size: int64(len(img))},
		"another hash": {Name: "a.png", MimeType: "image/png", Hash: Hash([]byte("x")), Size: int64(len(img))},
		"no hash":      {Name: "a.png", MimeType: "image/png", Hash: "../x", Size: int64(len(img))},
	} {
		if _, err := u.Take(bad); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	data, err := u.Take(want)
	if err != nil || !bytes.Equal(data, img) {
		t.Fatalf("Take = %d bytes, %v", len(data), err)
	}
	if err := u.Keep(data); err != nil {
		t.Fatal(err)
	}
	if kept, err := store.Get(want.Hash); err != nil || !bytes.Equal(kept, img) {
		t.Errorf("the store has %d bytes, %v", len(kept), err)
	}
	// Once kept, the image is the store's, and a later prompt takes it
	// from there.
	if _, err := os.Stat(filepath.Join(dir, "uploads", want.Hash)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the upload is still waiting: %v", err)
	}
	if again, err := u.Take(want); err != nil || !bytes.Equal(again, img) {
		t.Errorf("Take after Keep = %d bytes, %v", len(again), err)
	}
}

func TestUploadsRefuseWhatIsNotAnImage(t *testing.T) {
	u := NewUploads(t.TempDir(), New(t.TempDir()))
	text := []byte("not an image at all")
	if _, err := u.Receive(v1.UploadPiece{Upload: "Upload0002", Data: text, Size: int64(len(text)), MimeType: "image/png"}); err == nil {
		t.Error("text called a PNG was taken")
	}
	if _, err := u.Receive(v1.UploadPiece{Upload: "Upload0002", Data: text, Size: int64(len(text)), MimeType: "text/plain"}); err == nil {
		t.Error("text was taken")
	}
	if _, err := u.Receive(v1.UploadPiece{Upload: "../../etc", Data: png(1), Size: 9, MimeType: "image/png"}); err == nil {
		t.Error("an upload named like a path was taken")
	}
	if _, err := u.Receive(v1.UploadPiece{Upload: "Upload0003", Data: png(1), Size: v1.MaxImage + 1, MimeType: "image/png"}); err == nil {
		t.Error("an image past the limit was taken")
	}
	if _, err := u.Take(v1.PromptImage{Name: "x.png", MimeType: "image/png", Hash: Hash(png(1)), Size: 9}); !errors.Is(err, ErrNotUploaded) {
		t.Errorf("Take of an image never uploaded = %v, want ErrNotUploaded", err)
	}
}

func TestSweepRemovesUploadsNobodySent(t *testing.T) {
	dir := t.TempDir()
	u := NewUploads(dir, New(t.TempDir()))
	img := png(10)
	whole, err := u.Receive(v1.UploadPiece{Upload: "Upload0004", Data: img, Size: int64(len(img)), MimeType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Receive(v1.UploadPiece{Upload: "Upload0005", Data: img[:4], Size: int64(len(img)), MimeType: "image/png"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if n := u.Sweep(now, UploadsExpire); n != 0 {
		t.Errorf("a fresh sweep removed %d", n)
	}
	if n := u.Sweep(now.Add(UploadsExpire+time.Minute), UploadsExpire); n != 2 {
		t.Errorf("an hour later the sweep removed %d, want the image and the unfinished upload", n)
	}
	if _, err := u.Take(v1.PromptImage{Name: "a.png", MimeType: "image/png", Hash: whole.Hash, Size: int64(len(img))}); !errors.Is(err, ErrNotUploaded) {
		t.Errorf("Take after the sweep = %v, want ErrNotUploaded", err)
	}
}
