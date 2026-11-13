package citadel

import (
	"bytes"
	"cmp"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/themepack"
)

// themes is the world's theme packs as this citadel last read them from
// the bucket, each checked again as it was read.
type themes struct {
	mu      sync.Mutex
	packs   map[string]*themepack.Pack
	watched sync.Once
}

// list is the packs as browsers are told of them, by name.
func (t *themes) list() []web.ThemeInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]web.ThemeInfo, 0, len(t.packs))
	for _, p := range t.packs {
		m := p.Manifest
		out = append(out, web.ThemeInfo{ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description, Hash: p.Hash})
	}
	slices.SortFunc(out, func(a, b web.ThemeInfo) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	return out
}

// keepOnly drops every pack whose id isn't in ids, and says whether any
// was dropped.
func (t *themes) keepOnly(ids map[string]bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	dropped := false
	for id := range t.packs {
		if !ids[id] {
			delete(t.packs, id)
			dropped = true
		}
	}
	return dropped
}

func (t *themes) get(id string) *themepack.Pack {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.packs[id]
}

// set keeps p under id, or drops what is there when p is nil, and says
// whether anything changed.
func (t *themes) set(id string, p *themepack.Pack) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	had := t.packs[id]
	if p == nil {
		delete(t.packs, id)
		return had != nil
	}
	if t.packs == nil {
		t.packs = map[string]*themepack.Pack{}
	}
	t.packs[id] = p
	return had == nil || had.Hash != p.Hash
}

// watchThemes follows the world's theme bucket, from the first time the
// citadel reaches JetStream, and tells browsers whenever its packs change.
// It keeps following through reconnections.
func (c *citadel) watchThemes(js jetstream.JetStream) {
	c.themes.watched.Do(func() { go c.followThemes(js) })
}

// followThemes watches the bucket until the citadel stops, watching again
// whenever a watch can't start or ends.
func (c *citadel) followThemes(js jetstream.JetStream) {
	for {
		if err := c.watchThemesOnce(js); err != nil {
			slog.Warn("citadel: watch the theme bucket", "bucket", themepack.Bucket, "err", err)
		}
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// watchThemesOnce reads every pack in the bucket, drops the packs it no
// longer holds, and then follows its changes until the watch ends.
func (c *citadel) watchThemesOnce(js jetstream.JetStream) error {
	store, err := themepack.OpenStore(c.ctx, js)
	if err != nil {
		return err
	}
	w, err := store.Watch(c.ctx)
	if err != nil {
		return err
	}
	defer func() { _ = w.Stop() }()
	// seen is what the bucket holds as the watch begins, until nil marks
	// the end of it.
	seen := map[string]bool{}
	for {
		select {
		case <-c.ctx.Done():
			return nil
		case info, ok := <-w.Updates():
			if !ok {
				return nil
			}
			changed := false
			switch {
			case info == nil && seen != nil:
				changed = c.themes.keepOnly(seen)
				seen = nil
			case info == nil:
			default:
				if seen != nil {
					seen[info.Name] = true
				}
				changed = c.themes.set(info.Name, c.readTheme(store, info))
			}
			if changed {
				c.web.Broadcast(web.Message{Type: "themes", Themes: c.themes.list()})
			}
		}
	}
}

// readTheme reads and checks the pack an update names, or returns nil if
// it was deleted or isn't a valid pack.
func (c *citadel) readTheme(store jetstream.ObjectStore, info *jetstream.ObjectInfo) *themepack.Pack {
	if info.Deleted {
		return nil
	}
	p, err := themepack.Get(c.ctx, store, info.Name)
	if err != nil {
		slog.Warn("citadel: theme pack refused", "id", info.Name, "err", err)
		return nil
	}
	slog.Info("citadel: theme pack", "id", p.Manifest.ID, "name", p.Manifest.Name, "version", p.Manifest.Version, "sha256", p.Hash)
	return p
}

// serveTheme serves one file of a pack, at /themes/<id>/<hash>/<file>.
// The hash in the path is the pack's, so the file can be kept for good,
// and a pack replaced since is a new path. Files are data the page reads,
// never pages, so a browser opening one runs nothing from it.
func (c *citadel) serveTheme(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/themes/"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	id, hash, file := parts[0], parts[1], parts[2]
	types := map[string]string{themepack.ManifestFile: "application/json", themepack.ModelsFile: "model/gltf-binary"}
	ctype, ok := types[file]
	p := c.themes.get(id)
	if !ok || p == nil || p.Hash != hash {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, file, time.Time{}, bytes.NewReader(p.Files[file]))
}
