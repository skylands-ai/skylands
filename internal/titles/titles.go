// Package titles asks an agent to name a conversation, and cleans up what
// it says. The daemon sends the request in a side session so the title
// never appears in the conversation itself.
package titles

import (
	"strings"
	"unicode"
)

// marker opens every title request, so a test agent can tell one apart.
const marker = "Give this conversation a title."

// MaxLen is the longest title kept, in characters.
const MaxLen = 72

// Request builds the prompt that asks for a title of the conversation that
// begins with the given request.
func Request(first string) string {
	first = strings.TrimSpace(first)
	if len(first) > 1500 {
		first = first[:1500] + "…"
	}
	return marker + " Reply with only the title: at most six words, plain text, no quotes, no period, no Markdown. Do not run any tools or read any files. The conversation begins with this request:\n\n" + first
}

// IsRequest reports whether a prompt is a title request.
func IsRequest(prompt string) bool {
	return strings.HasPrefix(strings.TrimSpace(prompt), marker)
}

// RequestedFor returns the request a title prompt is about.
func RequestedFor(prompt string) string {
	_, after, ok := strings.Cut(prompt, "request:\n\n")
	if !ok {
		return ""
	}
	return after
}

// Clean turns an agent's reply into a title, or returns "" when the reply
// is unusable: empty, several paragraphs long, or longer than fits.
func Clean(reply string) string {
	line := ""
	for _, l := range strings.Split(reply, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	line = strings.TrimPrefix(line, "Title:")
	line = strings.Trim(line, " \t\"'`*_#")
	line = strings.TrimRight(line, ".")
	line = strings.TrimSpace(line)
	if line == "" || len([]rune(line)) > MaxLen*2 {
		return ""
	}
	return Cut(line)
}

// Cut shortens a title to MaxLen characters with an ellipsis.
func Cut(s string) string {
	r := []rune(s)
	if len(r) <= MaxLen {
		return s
	}
	return strings.TrimRightFunc(string(r[:MaxLen-1]), unicode.IsSpace) + "…"
}

// Fake is the title a test agent gives: the request's first four words,
// capitalized, which is enough to tell a generated title from the prompt.
func Fake(request string) string {
	words := strings.Fields(strings.SplitN(strings.TrimSpace(request), "\n", 2)[0])
	if len(words) > 4 {
		words = words[:4]
	}
	t := strings.Join(words, " ")
	if t == "" {
		return "Untitled"
	}
	r := []rune(t)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
