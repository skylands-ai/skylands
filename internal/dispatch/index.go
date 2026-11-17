// Package dispatch chooses where work asked for at the world or an island
// goes. Each daemon keeps an Entry for each of its repositories and ranks
// them against a prompt by keyword (Rank), and a citadel compares what
// the islands answer (Decide). A close call between repositories on one
// island goes to that island's agent (PickQuestion), and anything else
// unsettled goes to a person.
//
// A choice among a few labeled repositories is a short ranking, which
// keyword matching usually gets right: "add multi-currency support to
// pricing" matches internal/pricing/. It needs no embedding model on the
// host and sends nothing off it.
package dispatch

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// tier is the kind of place a word matched, which sets its weight.
type tier int

const (
	tierText tier = iota + 1
	tierPath
	tierName
)

// weight is what a word that matched in a tier adds to a confidence,
// before it is divided by the prompt's meaningful words.
func (t tier) weight() float64 {
	switch t {
	case tierName:
		return 1
	case tierPath:
		return 0.7
	case tierText:
		return 0.4
	}
	return 0
}

// partial is what a word is worth, as a share of its tier's weight, when
// it only begins a term or a term begins it, as "auth" and
// "authentication" do. Neither may be shorter than minPrefix.
const (
	partial   = 0.75
	minPrefix = 4
)

// Source is what a daemon gathers about one repository, from which its
// Entry is built.
type Source struct {
	ID   string
	Name string
	// Modules are the modules and packages the repository declares.
	Modules []Module
	// Paths are its tracked files, relative to the checkout.
	Paths []string
	// Readme is the first section of its README.
	Readme string
	// Commits are its recent commit subjects, newest first.
	Commits []string
}

// Module is a module or package a repository declares, such as a Go
// module, an npm package, or a crate, with the manifest that declares it.
type Module struct {
	Name     string
	Manifest string
}

// Title is the title an earlier conversation on a repository was given.
type Title struct {
	Session string
	Title   string
}

// place is where a term was found, and what a reason says of it.
type place struct {
	tier  tier
	where string
}

// Entry is one repository in a daemon's index: the terms it holds and
// where each was found, by stem.
type Entry struct {
	ID   string
	Name string
	// Modules are the repository's modules, for naming and describing it.
	Modules []Module
	terms   map[string]place
	// sorted holds the terms in order, for finding those a word begins.
	sorted []string
	// top lists the checkout's top-level entries, and readme the start of
	// its README, which describe it to an agent breaking a tie.
	top    []string
	readme string
}

// skippedDirs are directories whose files say nothing of the repository's
// own work: vendored and generated code.
var skippedDirs = map[string]bool{"vendor": true, "node_modules": true, "third_party": true, "dist": true}

// Build indexes a repository from what was gathered about it.
func Build(src Source) *Entry {
	e := &Entry{ID: src.ID, Name: src.Name, Modules: src.Modules, terms: map[string]place{}}
	for _, p := range parts(src.Name) {
		e.add(p, place{tierName, "its name"})
	}
	for _, m := range src.Modules {
		for _, p := range parts(m.Name) {
			e.add(p, place{tierName, "the module " + m.Name})
		}
	}
	tops := map[string]bool{}
	for _, path := range src.Paths {
		segs := strings.Split(path, "/")
		if skippedUnder(segs) {
			continue
		}
		top := segs[0]
		if len(segs) > 1 {
			top += "/"
		}
		if !tops[top] {
			tops[top] = true
			e.top = append(e.top, top)
		}
		for i, seg := range segs {
			where := strings.Join(segs[:i+1], "/")
			if i < len(segs)-1 {
				where += "/"
			} else if dot := strings.LastIndexByte(seg, '.'); dot > 0 {
				// A file's extension says what language it is in, which
				// most repositories share.
				seg = seg[:dot]
			}
			for _, p := range parts(seg) {
				e.add(p, place{tierPath, where})
			}
		}
	}
	sort.Strings(e.top)
	readme := plainText(src.Readme)
	for _, p := range parts(readme) {
		e.add(p, place{tierText, "the README"})
	}
	e.readme = cut(strings.Join(strings.Fields(readme), " "), 400)
	for _, subject := range src.Commits {
		for _, p := range parts(subject) {
			e.add(p, place{tierText, fmt.Sprintf("a commit, “%s”", cut(subject, 60))})
		}
	}
	e.sorted = make([]string, 0, len(e.terms))
	for t := range e.terms {
		e.sorted = append(e.sorted, t)
	}
	sort.Strings(e.sorted)
	return e
}

// skippedUnder reports whether a path is inside a vendored or generated
// directory.
func skippedUnder(segs []string) bool {
	for _, s := range segs[:len(segs)-1] {
		if skippedDirs[s] {
			return true
		}
	}
	return false
}

// add records that the part was found at pl, keeping the best place for
// its stem: the higher tier, or within paths, the shorter path, which is
// the directory the term names rather than a file deep inside it.
func (e *Entry) add(part string, pl place) {
	if len(part) < 2 || onlyDigits(part) {
		return
	}
	s := stem(part)
	got, ok := e.terms[s]
	if !ok || pl.tier > got.tier || pl.tier == got.tier && pl.tier == tierPath && len(pl.where) < len(got.where) {
		e.terms[s] = pl
	}
}

func onlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var (
	markdownImage = regexp.MustCompile(`!\[[^\]]*\](\([^)]*\))?`)
	markdownLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	bareURL       = regexp.MustCompile(`https?://\S+`)
	htmlTag       = regexp.MustCompile(`<[^>]+>`)
	heading       = regexp.MustCompile(`(?m)^\s*#+\s*`)
)

// plainText strips what a README carries besides its words: images, such
// as badges, links' targets, bare URLs, HTML, and heading marks.
func plainText(s string) string {
	s = markdownImage.ReplaceAllString(s, " ")
	s = markdownLink.ReplaceAllString(s, "$1")
	s = bareURL.ReplaceAllString(s, " ")
	s = htmlTag.ReplaceAllString(s, " ")
	return heading.ReplaceAllString(s, "")
}

// cut shortens s to at most n runes, with an ellipsis.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// lookup finds a stem among the entry's terms and the given titles' terms,
// and returns the weight it earns and the place it was found. A term it
// only begins, or one that begins it, earns less.
func (e *Entry) lookup(s string, titles map[string]place) (float64, place) {
	best, at := 0.0, place{}
	try := func(pl place, ok bool, share float64) {
		if ok && pl.tier.weight()*share > best {
			best, at = pl.tier.weight()*share, pl
		}
	}
	pl, ok := e.terms[s]
	try(pl, ok, 1)
	pl, ok = titles[s]
	try(pl, ok, 1)
	if best == tierName.weight() || len(s) < minPrefix {
		return best, at
	}
	// Terms the stem begins, best tier first; a stem found exactly only in
	// text may still begin a path.
	i := sort.SearchStrings(e.sorted, s)
	for n := 0; i < len(e.sorted) && n < 200 && strings.HasPrefix(e.sorted[i], s); i, n = i+1, n+1 {
		if e.sorted[i] != s {
			try(e.terms[e.sorted[i]], true, partial)
		}
	}
	// Terms that begin the stem.
	for l := len(s) - 1; l >= minPrefix; l-- {
		pl, ok := e.terms[s[:l]]
		try(pl, ok, partial)
	}
	return best, at
}

// Score says how much of the prompt the repository accounts for, and why.
// titles are the titles of earlier conversations on it, except the one
// for the session named by skip, so a past session's prompt can be
// scored without its own title.
func (e *Entry) Score(p Prompt, titles []Title, skip string) v1.MatchCandidate {
	c := v1.MatchCandidate{RepoID: e.ID, Name: e.Name}
	// The most specific name the prompt uses is the one the reason gives:
	// "storefront-pricing" names the module rather than storefront.
	reason, most := "", 0
	if p.Names(e.Name) {
		reason, most = "the prompt names it", len(parts(e.Name))
	}
	for _, m := range e.Modules {
		if n := len(parts(m.Name)); n > most && p.Names(m.Name) {
			reason, most = "the prompt names its module "+m.Name+" ("+m.Manifest+")", n
		}
	}
	if reason != "" {
		c.Named, c.Confidence, c.Reasons = true, 1, []string{reason}
		return c
	}
	if p.Empty() {
		return c
	}
	titled := map[string]place{}
	for _, t := range titles {
		if t.Session == skip || t.Title == "" {
			continue
		}
		pl := place{tierText, fmt.Sprintf("the conversation “%s”", cut(t.Title, 60))}
		for _, part := range parts(t.Title) {
			if s := stem(part); len(part) >= 2 {
				if _, ok := titled[s]; !ok {
					titled[s] = pl
				}
			}
		}
	}
	type found struct {
		part   string
		weight float64
		at     place
	}
	var hits []found
	sum := 0.0
	for _, w := range p.words {
		best := found{}
		for i, s := range w.stems {
			if weight, at := e.lookup(s, titled); weight > best.weight {
				best = found{w.parts[i], weight, at}
			}
		}
		if best.weight > 0 {
			sum += best.weight
			hits = append(hits, best)
		}
	}
	c.Confidence = math.Min(1, math.Round(sum/float64(len(p.words))*100)/100)
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].weight > hits[j].weight })
	for _, h := range hits {
		if len(c.Reasons) == 3 {
			break
		}
		c.Reasons = append(c.Reasons, h.part+" in "+h.at.where)
	}
	return c
}

// Rank scores the prompt against every entry and returns the best
// MaxCandidates that it touches at all, best first: named ones, then by
// confidence, then by name. titles are each repository's earlier
// conversations by id, and skip a session whose title is left out.
func Rank(entries []*Entry, p Prompt, titles map[string][]Title, skip string) []v1.MatchCandidate {
	var out []v1.MatchCandidate
	for _, e := range entries {
		if c := e.Score(p, titles[e.ID], skip); c.Named || c.Confidence > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return better(out[i], out[j]) })
	if len(out) > v1.MaxCandidates {
		out = out[:v1.MaxCandidates]
	}
	return out
}

// better orders candidates: named first, then by confidence, then by name.
func better(a, b v1.MatchCandidate) bool {
	if a.Named != b.Named {
		return a.Named
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	return a.Name < b.Name
}
