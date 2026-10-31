package titles

import (
	"strings"
	"testing"
)

func TestRequestRoundTrip(t *testing.T) {
	p := Request("tidy the readme\nand more")
	if !IsRequest(p) || IsRequest("tidy the readme") {
		t.Error("IsRequest misjudges")
	}
	if got := RequestedFor(p); got != "tidy the readme\nand more" {
		t.Errorf("RequestedFor = %q", got)
	}
	if got := Fake(RequestedFor(p)); got != "Tidy the readme" {
		t.Errorf("Fake = %q", got)
	}
	if got := Fake("one two three four five six"); got != "One two three four" {
		t.Errorf("Fake cut = %q", got)
	}
}

func TestClean(t *testing.T) {
	cases := map[string]string{
		"Tidy the README":            "Tidy the README",
		"\"Tidy the README.\"\n":     "Tidy the README",
		"Title: **Tidy the README**": "Tidy the README",
		"\n\n  Shorten quickstart  ": "Shorten quickstart",
		"":                           "",
		strings.Repeat("word ", 60):  "",
		strings.Repeat("w", 80):      strings.Repeat("w", 71) + "…",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
