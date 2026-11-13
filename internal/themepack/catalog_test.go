package themepack

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/skylands-ai/skylands/internal/themepack/packtest"
)

// listing is the test pack as a repository lists it, under its real hash.
func listing(t *testing.T) packtest.Listing {
	t.Helper()
	files := packtest.Files(t, packtest.NewDoc(), packtest.Manifest)
	return packtest.Listing{
		Entry: map[string]any{"id": "test-pack", "name": "Test Pack", "version": "1.2.3", "description": "Triangles, mostly", "author": "Tess", "license": "CC0-1.0", "sha256": Hash(files), "path": "test-pack"},
		Files: files,
	}
}

func TestCatalogFetchesAListedPack(t *testing.T) {
	srv := packtest.Repo(t, CatalogFormat, listing(t))
	ctx := context.Background()
	c, err := FetchCatalog(ctx, srv.Client(), srv.URL+"/index.json")
	if err != nil {
		t.Fatal(err)
	}
	l, ok := c.Find("test-pack")
	if !ok {
		t.Fatalf("Find: the catalog lists %+v", c.Packs)
	}
	p, err := c.Fetch(ctx, srv.Client(), l)
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.ID != "test-pack" || p.Hash != l.SHA256 {
		t.Errorf("fetched %s with sha256 %s", p.Manifest.ID, p.Hash)
	}
}

func TestCatalogMatching(t *testing.T) {
	srv := packtest.Repo(t, CatalogFormat, listing(t))
	c, err := FetchCatalog(context.Background(), srv.Client(), srv.URL+"/index.json")
	if err != nil {
		t.Fatal(err)
	}
	for words, want := range map[string]int{"": 1, "triangles": 1, "TEST tess": 1, "lanterns": 0, "test lanterns": 0} {
		if got := len(c.Matching(strings.Fields(words))); got != want {
			t.Errorf("Matching(%q) found %d, want %d", words, got, want)
		}
	}
}

func TestCatalogRefuses(t *testing.T) {
	ctx := context.Background()
	t.Run("files that don't have the listed hash", func(t *testing.T) {
		l := listing(t)
		l.Entry["sha256"] = strings.Repeat("0", 64)
		srv := packtest.Repo(t, CatalogFormat, l)
		c, err := FetchCatalog(ctx, srv.Client(), srv.URL+"/index.json")
		if err != nil {
			t.Fatal(err)
		}
		listed, _ := c.Find("test-pack")
		if _, err := c.Fetch(ctx, srv.Client(), listed); err == nil || !strings.Contains(err.Error(), "not the "+strings.Repeat("0", 64)) {
			t.Errorf("Fetch of files with another hash: %v", err)
		}
	})
	t.Run("a path outside the repository", func(t *testing.T) {
		l := listing(t)
		l.Entry["path"] = "../elsewhere"
		srv := packtest.Repo(t, CatalogFormat, l)
		c, err := FetchCatalog(ctx, srv.Client(), srv.URL+"/index.json")
		if err != nil {
			t.Fatal(err)
		}
		listed, _ := c.Find("test-pack")
		if _, err := c.Fetch(ctx, srv.Client(), listed); err == nil || !strings.Contains(err.Error(), "isn't a directory beside it") {
			t.Errorf("Fetch from a path outside the repository: %v", err)
		}
	})
	t.Run("a pack under another id", func(t *testing.T) {
		l := listing(t)
		l.Entry["id"] = "other-pack"
		srv := packtest.Repo(t, CatalogFormat, l)
		c, err := FetchCatalog(ctx, srv.Client(), srv.URL+"/index.json")
		if err != nil {
			t.Fatal(err)
		}
		listed, _ := c.Find("other-pack")
		if _, err := c.Fetch(ctx, srv.Client(), listed); err == nil || !strings.Contains(err.Error(), "says its id is test-pack") {
			t.Errorf("Fetch of a pack listed under another id: %v", err)
		}
	})
	t.Run("a later index format", func(t *testing.T) {
		srv := packtest.Repo(t, CatalogFormat+1, listing(t))
		if _, err := FetchCatalog(ctx, srv.Client(), srv.URL+"/index.json"); err == nil || !strings.Contains(err.Error(), "may need updating") {
			t.Errorf("FetchCatalog of a later format: %v", err)
		}
	})
	t.Run("plain http elsewhere", func(t *testing.T) {
		if _, err := FetchCatalog(ctx, http.DefaultClient, "http://example.com/index.json"); err == nil || !strings.Contains(err.Error(), "isn't an https URL") {
			t.Errorf("FetchCatalog over http: %v", err)
		}
	})
}
