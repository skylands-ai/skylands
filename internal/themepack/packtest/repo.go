package packtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Listing is one pack in a test themes repository: its index entry, as
// JSON values a test can break, and its files.
type Listing struct {
	Entry map[string]any
	Files map[string][]byte
}

// Repo serves a themes repository over TLS: index.json listing packs,
// and each pack's files under its entry's path, if that is a plain
// directory name. Its client trusts it.
func Repo(t testing.TB, format int, packs ...Listing) *httptest.Server {
	t.Helper()
	entries := make([]any, 0, len(packs))
	mux := http.NewServeMux()
	for _, p := range packs {
		entries = append(entries, p.Entry)
		path, _ := p.Entry["path"].(string)
		if strings.ContainsAny(path, "./") {
			// A path a test makes point outside the repository has no files.
			continue
		}
		for name, data := range p.Files {
			mux.HandleFunc("GET /"+path+"/"+name, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) })
		}
	}
	index, err := json.Marshal(map[string]any{"format": format, "packs": entries})
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("GET /index.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(index) })
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
