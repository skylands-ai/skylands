package dispatch

import (
	"math"
	"sort"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

// How confident a match must be. They are set against the replay of past
// sessions that skylands repo match --replay runs on a host.
const (
	// Clear is the confidence a repository needs to take the work alone.
	Clear = 0.35
	// Lead is how far ahead of the runner-up a clear winner must be. A
	// candidate within Lead of the best is in contention with it.
	Lead = 0.15
	// Fits is the least confidence that counts a repository as a fit.
	Fits = 0.15
)

// Candidate is a repository an island offered for a prompt.
type Candidate struct {
	Host string `json:"host"`
	v1.MatchCandidate
}

// Answer is what one island answered a match with.
type Answer struct {
	Host       string
	Candidates []v1.MatchCandidate
}

// What a Verdict does with the work.
const (
	// Start the work in Target.
	Start = "start"
	// Ask Host's agent to choose among Among, which are all on it.
	AskAgent = "agent"
	// Ask a person to choose, offering Among first.
	AskPerson = "person"
)

// Why a verdict is what it is.
const (
	// The prompt names the target, or names several, which are in Among.
	WhyNamed = "named"
	// The target matched well and well ahead of the rest.
	WhyClear = "clear"
	// The best candidates, in Among, are too close to tell apart.
	WhyClose = "close"
	// The only candidate that fits, in Among, matched too little to take
	// the work alone.
	WhyWeak = "weak"
	// Nothing fits.
	WhyNothing = "nothing"
	// The scope is one island with one repository, the target, so there
	// was nothing to choose between.
	WhyOnly = "only"
	// The prompt links to an issue, and the target is the repository it
	// is in, or Among are those that are, or none in scope is.
	WhyIssue = "issue"
)

// Verdict is where the work goes.
type Verdict struct {
	Do     string     `json:"do"`
	Why    string     `json:"why"`
	Target *Candidate `json:"target,omitempty"`
	// Host is the island whose agent breaks a tie.
	Host string `json:"host,omitempty"`
	// Among are the candidates a tie is between, or the ones a person is
	// offered first.
	Among []Candidate `json:"among,omitempty"`
	// Ranked is every candidate, best first.
	Ranked []Candidate `json:"ranked"`
}

// Decide compares what the islands answered. A repository the prompt
// names takes the work. Otherwise a clear winner does: confidence of at
// least Clear, and more than Lead ahead of the runner-up. Candidates too
// close to tell apart go to their island's agent when they are all on
// one island, and to a person when they are on several, since neither
// island's agent may see the other's repositories. So does a lone fit
// that matched too little, and so does nothing fitting at all.
func Decide(answers []Answer) Verdict {
	v := Verdict{Ranked: []Candidate{}}
	for _, a := range answers {
		for _, c := range a.Candidates {
			v.Ranked = append(v.Ranked, Candidate{Host: a.Host, MatchCandidate: c})
		}
	}
	sort.SliceStable(v.Ranked, func(i, j int) bool {
		a, b := v.Ranked[i], v.Ranked[j]
		if a.Named != b.Named || a.Confidence != b.Confidence || a.Name != b.Name {
			return better(a.MatchCandidate, b.MatchCandidate)
		}
		return a.Host < b.Host
	})
	var named, fits []Candidate
	for _, c := range v.Ranked {
		if c.Named {
			named = append(named, c)
		}
		if c.Named || c.Confidence >= Fits {
			fits = append(fits, c)
		}
	}
	switch {
	case len(named) == 1:
		v.Do, v.Why, v.Target = Start, WhyNamed, &named[0]
		return v
	case len(named) > 1:
		v.Why, v.Among = WhyNamed, named
		return v.among()
	case len(fits) == 0:
		v.Do, v.Why = AskPerson, WhyNothing
		return v
	}
	top := fits[0]
	runnerUp := 0.0
	if len(fits) > 1 {
		runnerUp = fits[1].Confidence
	}
	if top.Confidence >= Clear && gap(top.Confidence, runnerUp) > Lead {
		v.Do, v.Why, v.Target = Start, WhyClear, &fits[0]
		return v
	}
	for _, c := range fits {
		if gap(top.Confidence, c.Confidence) <= Lead {
			v.Among = append(v.Among, c)
		}
	}
	if len(v.Among) == 1 {
		v.Do, v.Why = AskPerson, WhyWeak
		return v
	}
	v.Why = WhyClose
	return v.among()
}

// among sends a tie to the agent of the island it is on, or to a person
// when it spans islands.
func (v Verdict) among() Verdict {
	v.Do = AskAgent
	v.Host = v.Among[0].Host
	for _, c := range v.Among[1:] {
		if c.Host != v.Host {
			v.Do, v.Host = AskPerson, ""
			break
		}
	}
	return v
}

// gap is how far a is ahead of b, to the hundredth that confidences are
// given in, so a lead of exactly Lead isn't more than Lead.
func gap(a, b float64) float64 { return math.Round((a-b)*100) / 100 }
