package themepack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CatalogURL is the index of the packs in the official themes repository,
// skylands-ai/themes, whose CI checks every pack and the index itself.
const CatalogURL = "https://raw.githubusercontent.com/skylands-ai/themes/main/index.json"

// CatalogFormat is the version of the index format this package reads.
const CatalogFormat = 1

// maxCatalog is the most an index may be.
const maxCatalog = 1 << 20

// Catalog is a themes repository's index of its packs. Each pack's files
// are beside the index, under the pack's path.
type Catalog struct {
	Format int      `json:"format"`
	Packs  []Listed `json:"packs"`
	// from is where the index was read, which pack paths are relative to.
	from *url.URL
}

// Listed is one pack as a catalog lists it.
type Listed struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	License     string `json:"license"`
	// SHA256 is the pack's hash, which its files must have.
	SHA256 string `json:"sha256"`
	// Path is the pack's directory, beside the index.
	Path string `json:"path"`
}

// IsID says whether s could be a pack's id.
func IsID(s string) bool {
	return idPattern.MatchString(s) && len(s) >= 2 && len(s) <= 40
}

// checkURL refuses anything but https, apart from http to this machine,
// for a mirror or a test.
func checkURL(u *url.URL) error {
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return fmt.Errorf("%s isn't an https URL", u.Redacted())
}

// get reads what a URL answers, refusing more than limit bytes.
func get(ctx context.Context, client *http.Client, u *url.URL, limit int) ([]byte, error) {
	if err := checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", u.Redacted(), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", u.Redacted(), limit)
	}
	return data, nil
}

// FetchCatalog reads the index at indexURL.
func FetchCatalog(ctx context.Context, client *http.Client, indexURL string) (*Catalog, error) {
	u, err := url.Parse(indexURL)
	if err != nil {
		return nil, err
	}
	data, err := get(ctx, client, u, maxCatalog)
	if err != nil {
		return nil, fmt.Errorf("read the themes index: %w", err)
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("read the themes index at %s: %w", u.Redacted(), err)
	}
	if c.Format != CatalogFormat {
		return nil, fmt.Errorf("the themes index at %s is format %d; this version of Skylands reads format %d, so it may need updating", u.Redacted(), c.Format, CatalogFormat)
	}
	c.from = u
	return &c, nil
}

// Find is the pack the catalog lists under id.
func (c *Catalog) Find(id string) (Listed, bool) {
	for _, l := range c.Packs {
		if l.ID == id {
			return l, true
		}
	}
	return Listed{}, false
}

// Matching is the packs whose id, name, description, or author has every
// one of words, ignoring case; every pack when there are no words.
func (c *Catalog) Matching(words []string) []Listed {
	var out []Listed
	for _, l := range c.Packs {
		text := strings.ToLower(strings.Join([]string{l.ID, l.Name, l.Description, l.Author}, " "))
		all := true
		for _, w := range words {
			if !strings.Contains(text, strings.ToLower(w)) {
				all = false
				break
			}
		}
		if all {
			out = append(out, l)
		}
	}
	return out
}

// Fetch downloads a listed pack's files from beside the index, checks
// them, and refuses them unless they have the hash the catalog lists.
func (c *Catalog) Fetch(ctx context.Context, client *http.Client, l Listed) (*Pack, error) {
	// A path is one directory beside the index, so a listing can't send
	// the download anywhere else.
	if !IsID(l.Path) {
		return nil, fmt.Errorf("the themes index lists %s at %q, which isn't a directory beside it", l.ID, l.Path)
	}
	files := map[string][]byte{}
	for _, name := range Files {
		u := c.from.ResolveReference(&url.URL{Path: l.Path + "/" + name})
		data, err := get(ctx, client, u, maxSize[name])
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", l.ID, err)
		}
		files[name] = data
	}
	p, err := Check(files)
	if err != nil {
		return nil, err
	}
	if p.Manifest.ID != l.ID {
		return nil, fmt.Errorf("the themes index lists %s, but its theme.json says its id is %s", l.ID, p.Manifest.ID)
	}
	if !strings.EqualFold(p.Hash, l.SHA256) {
		return nil, fmt.Errorf("%s's files have sha256 %s, not the %s the themes index lists; if the pack was just updated, try again in a few minutes", l.ID, p.Hash, l.SHA256)
	}
	return p, nil
}
