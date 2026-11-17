package dispatch

import (
	"strings"
	"unicode"
)

// parts breaks text into lowercase runs of letters and digits, splitting
// camelCase too, so "PricingEngine.ts" is pricing, engine, and ts.
func parts(s string) []string {
	var out []string
	var b strings.Builder
	var prev rune
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				flush()
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			flush()
		}
		prev = r
	}
	flush()
	return out
}

// stem cuts a lowercase word down to a rough stem, so plurals and the
// usual endings meet: pricing, prices, priced, and price are all "pric".
// It is deliberately crude; a word that stems badly still matches itself.
func stem(w string) string {
	if len(w) <= 3 {
		return w
	}
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		w = w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "sses"):
		w = w[:len(w)-2]
	case strings.HasSuffix(w, "ches"), strings.HasSuffix(w, "shes"), strings.HasSuffix(w, "xes"), strings.HasSuffix(w, "zes"):
		w = w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		w = w[:len(w)-1]
	}
	switch {
	case strings.HasSuffix(w, "ing") && len(w) > 5:
		w = w[:len(w)-3]
	case strings.HasSuffix(w, "ed") && len(w) > 4:
		w = w[:len(w)-2]
	}
	if strings.HasSuffix(w, "e") && len(w) > 3 {
		w = w[:len(w)-1]
	}
	return w
}

// skipped are the words that say nothing about where work goes: English
// stopwords, and the words any request to a coding agent uses, such as
// "add", "fix", "test", and "support". They are dropped from a prompt
// before it is scored, and a name made only of them names nothing.
var skipped = func() map[string]bool {
	words := strings.Fields(`
a about above after again against all also am an and any are as at be because been before being below between both but by
can cannot could did do does doing done down during each either else etc even ever every few for from further get gets got
had has have having he her here hers him his how however i if in into is it its itself just let lets like may me might more
most much must my myself near need needs neither no nor not now of off on once only onto or other our ours out over own per
please rather really same she should so some something somewhere such than that the their theirs them then there these they
this those though through thus to too under until up upon us very via was way we well were what whatever when where whether
which while who whom whose why will with within without would yet you your yours
add adding added adds allow also bug bugs change changed changes clean cleanup code create creates currently delete do ensure enable feature features file files fix fixed fixes fixing for handle implement implementation
improve improvement issue issues make makes making maybe move new properly refactor remove rename replace repo repos
repository repositories rewrite set should show support supported supports sure task tasks test tested testing tests thing
things try update updated updates use used uses using want wants work works write`)
	m := make(map[string]bool, len(words)*2)
	for _, w := range words {
		m[w] = true
		m[stem(w)] = true
	}
	return m
}()

// meaningful reports whether a part of a prompt says something about
// where the work goes: not a skipped word, not a lone letter, and not
// only digits.
func meaningful(p string) bool {
	if len(p) < 2 || skipped[p] || skipped[stem(p)] {
		return false
	}
	for _, r := range p {
		if !unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// word is one word of a prompt, as written between spaces, with the
// stems of its meaningful parts. A compound such as "multi-currency" or
// "internal/pricing" is one word, which counts once, for its best part.
type word struct {
	parts []string
	stems []string
}

// Prompt is a prompt as the index scores it.
type Prompt struct {
	// words are the meaningful words, each once.
	words []word
	// seq is every part of the prompt in order, for finding names in it.
	seq []string
}

// ParsePrompt reads a prompt for scoring.
func ParsePrompt(text string) Prompt {
	var p Prompt
	seen := map[string]bool{}
	for field := range strings.FieldsSeq(text) {
		all := parts(field)
		p.seq = append(p.seq, all...)
		var w word
		for _, part := range all {
			if meaningful(part) {
				w.parts = append(w.parts, part)
				w.stems = append(w.stems, stem(part))
			}
		}
		if len(w.stems) == 0 {
			continue
		}
		key := strings.Join(w.stems, " ")
		if seen[key] {
			continue
		}
		seen[key] = true
		p.words = append(p.words, w)
	}
	return p
}

// Empty reports whether the prompt has nothing to score: no word in it
// says where the work goes.
func (p Prompt) Empty() bool { return len(p.words) == 0 }

// Names reports whether the prompt names name: the name's parts appear in
// it in order, together, as "storefront-pricing" or "storefront pricing"
// name the module storefront-pricing. A name made only of skipped words,
// or shorter than three letters, names nothing, since prompts use such
// words for other things.
func (p Prompt) Names(name string) bool {
	want := parts(name)
	if len(want) == 0 || len(strings.Join(want, "")) < 3 {
		return false
	}
	telling := false
	for _, w := range want {
		if meaningful(w) {
			telling = true
		}
	}
	if !telling {
		return false
	}
	for i := 0; i+len(want) <= len(p.seq); i++ {
		match := true
		for j, w := range want {
			if p.seq[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
