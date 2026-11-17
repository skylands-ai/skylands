package dispatch

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// pickMarker opens every request to pick a repository, so a test agent
// can tell one apart.
const pickMarker = "Choose the repository this work is for."

// Describe is how a repository is described to an agent breaking a tie:
// its name, its modules, its top-level entries, the start of its README,
// and what in it the prompt matched. It holds nothing else from the
// repository.
func (e *Entry) Describe(reasons []string) string {
	var b strings.Builder
	b.WriteString(e.Name)
	if len(e.Modules) > 0 {
		var names []string
		for i, m := range e.Modules {
			if i == 6 {
				names = append(names, "…")
				break
			}
			names = append(names, m.Name)
		}
		fmt.Fprintf(&b, "\n   Modules: %s", strings.Join(names, ", "))
	}
	if len(e.top) > 0 {
		top := e.top
		if len(top) > 16 {
			top = append(top[:16:16], "…")
		}
		fmt.Fprintf(&b, "\n   Top level: %s", strings.Join(top, " "))
	}
	if e.readme != "" {
		fmt.Fprintf(&b, "\n   README: %s", e.readme)
	}
	if len(reasons) > 0 {
		fmt.Fprintf(&b, "\n   Matched: %s", strings.Join(reasons, "; "))
	}
	return b.String()
}

// PickQuestion asks an agent which of the described repositories the
// work is for, by number, or 0 when it can't tell.
func PickQuestion(work string, described []string) string {
	work = strings.TrimSpace(work)
	if len(work) > 2000 {
		work = work[:2000] + "…"
	}
	var b strings.Builder
	b.WriteString(pickMarker + " Someone asked for this work:\n\n")
	b.WriteString(work)
	b.WriteString("\n\nIt is for one of these repositories:\n\n")
	for i, d := range described {
		fmt.Fprintf(&b, "%d. %s\n", i+1, d)
	}
	b.WriteString("\nReply with only the number of the repository the work is for, or 0 if you can't tell. Do not run any tools or read any files.")
	return b.String()
}

// IsPickQuestion reports whether a prompt asks to pick a repository.
func IsPickQuestion(prompt string) bool {
	return strings.HasPrefix(strings.TrimSpace(prompt), pickMarker)
}

// ParsePick reads an agent's answer to a question offering n
// repositories: the number it chose, from 1, or 0 when it couldn't tell
// or the answer isn't one of them.
func ParsePick(reply string, n int) int {
	reply = strings.TrimLeft(strings.TrimSpace(reply), "*_`'\"# ")
	end := strings.IndexFunc(reply, func(r rune) bool { return !unicode.IsDigit(r) })
	if end < 0 {
		end = len(reply)
	}
	i, err := strconv.Atoi(reply[:end])
	if err != nil || i < 1 || i > n {
		return 0
	}
	// "2." or "2 – storefront" is a choice; "2 of them fit" isn't.
	if rest := strings.TrimSpace(reply[end:]); rest != "" {
		if r, _ := utf8.DecodeRuneInString(rest); !strings.ContainsRune(".):-–—*_`'\"", r) {
			return 0
		}
	}
	return i
}

// FakePick is what a test agent answers a pick question with: the first
// repository whose name the work mentions, or 0.
func FakePick(question string) string {
	_, rest, _ := strings.Cut(question, "asked for this work:\n\n")
	work, list, _ := strings.Cut(rest, "\n\nIt is for one of these repositories:\n\n")
	p := ParsePrompt(work)
	for line := range strings.SplitSeq(list, "\n") {
		num, name, ok := strings.Cut(line, ". ")
		if !ok || num == "" || strings.HasPrefix(line, " ") {
			continue
		}
		if p.mentions(name) {
			return num
		}
	}
	return "0"
}

// mentions reports whether any of name's parts is in the prompt.
func (p Prompt) mentions(name string) bool {
	for _, want := range parts(name) {
		if slices.Contains(p.seq, want) {
			return true
		}
	}
	return false
}
