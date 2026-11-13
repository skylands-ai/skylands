package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skylands-ai/skylands/internal/natsembed"
	"github.com/skylands-ai/skylands/internal/themepack"
	"github.com/skylands-ai/skylands/internal/themepack/packtest"
)

// writePack writes a pack's files into a new directory, with a README
// that isn't part of the pack.
func writePack(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range packtest.Files(t, packtest.NewDoc(), manifest) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestThemeCheck(t *testing.T) {
	dir := writePack(t, packtest.Manifest)
	code, out, errOut := run(t, "theme", "check", dir)
	if code != 0 {
		t.Fatalf("theme check: %d %s", code, errOut)
	}
	for _, want := range []string{"Test Pack 1.2.3 (test-pack), CC0-1.0", "1 triangles", "sha256 ", "left out: README.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("theme check's output lacks %q:\n%s", want, out)
		}
	}

	bad := writePack(t, strings.Replace(packtest.Manifest, `"id": "test-pack"`, `"id": "default"`, 1))
	code, _, errOut = run(t, "theme", "check", bad)
	if code == 0 || !strings.Contains(errOut, "built-in") {
		t.Errorf("theme check took a pack named for a built-in theme: %d %s", code, errOut)
	}
}

func TestThemeAddListRemove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "world", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	url := srv.ClientURL()

	code, out, errOut := run(t, "theme", "list", "--nats-url", url)
	if code != 0 || !strings.Contains(out, "No theme packs") {
		t.Fatalf("theme list before any pack: %d %q %s", code, out, errOut)
	}

	dir := writePack(t, packtest.Manifest)
	p, _, err := themepack.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	code, _, errOut = run(t, "theme", "add", dir, "--nats-url", url, "--sha256", strings.Repeat("0", 64))
	if code == 0 || !strings.Contains(errOut, "not "+strings.Repeat("0", 64)) {
		t.Errorf("theme add took a pack whose hash isn't the one asked for: %d %s", code, errOut)
	}
	code, out, errOut = run(t, "theme", "add", dir, "--nats-url", url, "--sha256", p.Hash)
	if code != 0 || !strings.Contains(out, "Added Test Pack 1.2.3 (test-pack), sha256 "+p.Hash) {
		t.Fatalf("theme add: %d %q %s", code, out, errOut)
	}
	code, out, _ = run(t, "theme", "add", dir, "--nats-url", url)
	if code != 0 || !strings.Contains(out, "already in the world") {
		t.Errorf("theme add of the same pack again: %d %q", code, out)
	}

	newer := writePack(t, strings.Replace(packtest.Manifest, `"version": "1.2.3"`, `"version": "1.3.0"`, 1))
	code, _, errOut = run(t, "theme", "add", newer, "--nats-url", url)
	if code == 0 || !strings.Contains(errOut, "pass --replace") {
		t.Errorf("theme add replaced a pack without --replace: %d %s", code, errOut)
	}
	code, out, errOut = run(t, "theme", "add", newer, "--nats-url", url, "--replace")
	if code != 0 || !strings.Contains(out, "Replaced 1.2.3 with Test Pack 1.3.0") {
		t.Fatalf("theme add --replace: %d %q %s", code, out, errOut)
	}

	code, out, _ = run(t, "theme", "list", "--nats-url", url)
	if code != 0 || !strings.Contains(out, "test-pack") || !strings.Contains(out, "1.3.0") {
		t.Errorf("theme list: %d %q", code, out)
	}

	code, out, errOut = run(t, "theme", "remove", "test-pack", "--nats-url", url)
	if code != 0 || !strings.Contains(out, "Removed Test Pack 1.3.0") {
		t.Fatalf("theme remove: %d %q %s", code, out, errOut)
	}
	code, _, errOut = run(t, "theme", "remove", "test-pack", "--nats-url", url)
	if code == 0 || !strings.Contains(errOut, `no theme pack "test-pack"`) {
		t.Errorf("theme remove of a pack that isn't there: %d %s", code, errOut)
	}
	code, out, _ = run(t, "theme", "list", "--nats-url", url)
	if code != 0 || !strings.Contains(out, "No theme packs") {
		t.Errorf("theme list after remove: %d %q", code, out)
	}
}

func TestThemeAddRefusesPlainHTTP(t *testing.T) {
	code, _, errOut := run(t, "theme", "add", "http://example.com/pack.zip")
	if code == 0 || !strings.Contains(errOut, "https") {
		t.Errorf("theme add over http: %d %s", code, errOut)
	}
}

// useRepo serves a themes repository listing the test pack, and points
// the CLI's downloads at it.
func useRepo(t *testing.T) (index string, hash string) {
	t.Helper()
	files := packtest.Files(t, packtest.NewDoc(), packtest.Manifest)
	hash = themepack.Hash(files)
	srv := packtest.Repo(t, themepack.CatalogFormat, packtest.Listing{
		Entry: map[string]any{"id": "test-pack", "name": "Test Pack", "version": "1.2.3", "description": "Triangles, mostly", "license": "CC0-1.0", "sha256": hash, "path": "test-pack"},
		Files: files,
	})
	was := webClient
	webClient = srv.Client()
	t.Cleanup(func() { webClient = was })
	return srv.URL + "/index.json", hash
}

func TestThemeSearch(t *testing.T) {
	index, _ := useRepo(t)
	code, out, errOut := run(t, "theme", "search", "--catalog", index)
	if code != 0 {
		t.Fatalf("theme search: %d %s", code, errOut)
	}
	for _, want := range []string{"test-pack", "Test Pack", "1.2.3", "Triangles, mostly", "skylands theme add <id>"} {
		if !strings.Contains(out, want) {
			t.Errorf("theme search's output lacks %q:\n%s", want, out)
		}
	}
	code, out, _ = run(t, "theme", "search", "triangles", "--catalog", index)
	if code != 0 || !strings.Contains(out, "test-pack") {
		t.Errorf("theme search triangles: %d %q", code, out)
	}
	code, out, _ = run(t, "theme", "search", "lanterns", "--catalog", index)
	if code != 0 || !strings.Contains(out, `No pack in the themes repository matches "lanterns"`) {
		t.Errorf("theme search lanterns: %d %q", code, out)
	}
}

func TestThemeAddByID(t *testing.T) {
	index, hash := useRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := natsembed.Start(ctx, natsembed.Config{Name: "world", ClientAddr: "127.0.0.1:0", StoreDir: t.TempDir(), Domain: "world"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()
	// Run from an empty directory, so the id names no directory here.
	t.Chdir(t.TempDir())

	code, out, errOut := run(t, "theme", "add", "test-pack", "--catalog", index, "--nats-url", srv.ClientURL())
	if code != 0 || !strings.Contains(out, "Downloading Test Pack 1.2.3") || !strings.Contains(out, "Added Test Pack 1.2.3 (test-pack), sha256 "+hash) {
		t.Fatalf("theme add test-pack: %d %q %s", code, out, errOut)
	}
	code, _, errOut = run(t, "theme", "add", "no-such-pack", "--catalog", index, "--nats-url", srv.ClientURL())
	if code == 0 || !strings.Contains(errOut, "skylands theme search lists those") {
		t.Errorf("theme add of an id the repository doesn't list: %d %s", code, errOut)
	}
}
