package themepack

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// Bucket is the object store, in the world's JetStream domain, that holds
// the world's installed packs, each zipped under its id. Every citadel
// serves what is in it, so a pack added once shows on every citadel.
const Bucket = "skylands-themes"

// Installed is what the bucket says about one pack without reading it.
type Installed struct {
	ID      string
	Name    string
	Version string
	Hash    string
	Size    uint64
}

// OpenStore opens the bucket, creating it if no one has yet.
func OpenStore(ctx context.Context, js jetstream.JetStream) (jetstream.ObjectStore, error) {
	return js.CreateOrUpdateObjectStore(ctx, jetstream.ObjectStoreConfig{
		Bucket:      Bucket,
		Description: "The Skylands world's installed theme packs",
		Storage:     jetstream.FileStorage,
	})
}

// ExistingStore opens the bucket, or returns nil when no pack was ever
// installed.
func ExistingStore(ctx context.Context, js jetstream.JetStream) (jetstream.ObjectStore, error) {
	store, err := js.ObjectStore(ctx, Bucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, nil
	}
	return store, err
}

// Put stores a checked pack under its id, in place of any pack there.
func Put(ctx context.Context, store jetstream.ObjectStore, p *Pack) error {
	data, err := p.Archive()
	if err != nil {
		return err
	}
	m := p.Manifest
	_, err = store.Put(ctx, jetstream.ObjectMeta{
		Name:        m.ID,
		Description: m.Name,
		Metadata:    map[string]string{"name": m.Name, "version": m.Version, "hash": p.Hash},
	}, bytes.NewReader(data))
	return err
}

// Get reads and checks the pack stored under id. A stored pack is checked
// again on every read, so one written to the bucket by other means is
// refused all the same.
func Get(ctx context.Context, store jetstream.ObjectStore, id string) (*Pack, error) {
	data, err := store.GetBytes(ctx, id)
	if err != nil {
		return nil, err
	}
	files, _, err := ReadZip(data)
	if err != nil {
		return nil, err
	}
	p, err := Check(files)
	if err != nil {
		return nil, err
	}
	if p.Manifest.ID != id {
		return nil, fmt.Errorf("the pack stored as %q says its id is %q", id, p.Manifest.ID)
	}
	return p, nil
}

// List is every installed pack, as the bucket describes it.
func List(ctx context.Context, store jetstream.ObjectStore) ([]Installed, error) {
	infos, err := store.List(ctx)
	if errors.Is(err, jetstream.ErrNoObjectsFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Installed, 0, len(infos))
	for _, info := range infos {
		out = append(out, installed(info))
	}
	return out, nil
}

func installed(info *jetstream.ObjectInfo) Installed {
	return Installed{ID: info.Name, Name: info.Metadata["name"], Version: info.Metadata["version"], Hash: info.Metadata["hash"], Size: info.Size}
}

// Lookup is what the bucket says about the pack under id, or false when
// there is none.
func Lookup(ctx context.Context, store jetstream.ObjectStore, id string) (Installed, bool, error) {
	info, err := store.GetInfo(ctx, id)
	if errors.Is(err, jetstream.ErrObjectNotFound) {
		return Installed{}, false, nil
	}
	if err != nil {
		return Installed{}, false, err
	}
	return installed(info), true, nil
}
