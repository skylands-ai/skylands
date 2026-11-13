package citadel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/skylands-ai/skylands/internal/citadel/web"
	"github.com/skylands-ai/skylands/internal/natsembed"
	"github.com/skylands-ai/skylands/internal/themepack"
	"github.com/skylands-ai/skylands/internal/themepack/packtest"
)

func TestCitadelServesTheWorldsThemePacks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "hub", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	addr := freeAddr(t)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- Run(runCtx, Config{NATSURL: srv.ClientURL(), HTTPAddr: addr}) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("citadel.Run returned %v", err)
		}
	}()
	base := "http://" + addr
	waitChecked(t, base)

	c, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.CloseNow() }()
	// next reads until a message carries the world's theme packs.
	next := func() []web.ThemeInfo {
		t.Helper()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var m web.Message
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if m.Type == "snapshot" || m.Type == "themes" {
				return m.Themes
			}
		}
	}
	if got := next(); len(got) != 0 {
		t.Fatalf("snapshot of a world with no packs lists %+v", got)
	}

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	store, err := themepack.OpenStore(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	p, err := themepack.Check(packtest.Files(t, packtest.NewDoc(), packtest.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	if err := themepack.Put(ctx, store, p); err != nil {
		t.Fatal(err)
	}
	got := next()
	if len(got) != 1 || got[0].ID != "test-pack" || got[0].Name != "Test Pack" || got[0].Hash != p.Hash {
		t.Fatalf("themes after adding a pack = %+v", got)
	}

	get := func(path string) (*http.Response, []byte) {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}
	resp, body := get(themepack.Served("test-pack", p.Hash, themepack.ModelsFile))
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, p.Files[themepack.ModelsFile]) {
		t.Fatalf("models.glb: %s, %d bytes", resp.Status, len(body))
	}
	h := resp.Header
	if h.Get("Content-Type") != "model/gltf-binary" || !strings.Contains(h.Get("Cache-Control"), "immutable") || h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("models.glb's headers = %v", h)
	}
	for _, path := range []string{
		themepack.Served("test-pack", strings.Repeat("0", 64), themepack.ModelsFile),
		themepack.Served("test-pack", p.Hash, themepack.LicenseFile),
		themepack.Served("other", p.Hash, themepack.ManifestFile),
		"/themes/test-pack/" + p.Hash + "/../../index.html",
	} {
		if resp, _ := get(path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %s, want 404", path, resp.Status)
		}
	}

	page, _ := get("/healthz")
	if csp := page.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("a page's Content-Security-Policy = %q", csp)
	}

	// Anything else put in the bucket is checked as it is read, and
	// refused if it isn't a pack.
	if _, err := store.PutBytes(ctx, "sneaky", []byte("<script>alert(1)</script>")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "test-pack"); err != nil {
		t.Fatal(err)
	}
	if got := next(); len(got) != 0 {
		t.Fatalf("themes after removing the pack = %+v", got)
	}
	if resp, _ := get(themepack.Served("sneaky", p.Hash, themepack.ManifestFile)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a refused object is served: %s", resp.Status)
	}
}
