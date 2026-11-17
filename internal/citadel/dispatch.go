package citadel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/citadel/projections"
	"github.com/skylands-ai/skylands/internal/dispatch"
)

// Work asked for at the world or an island goes where it fits, in two
// steps: the citadel picks the island, and the island's daemon picks the
// repository, by searching its own. A prompt that names a repository or an
// island settles it without scoring. Otherwise each island in scope scores
// the prompt against its own repositories, all at once, and the citadel
// compares what they answer. The browser shows where the work would go,
// and signs the start of a session there once the person confirms it, so
// nothing starts on the citadel's choice alone.

// dispatchRequest is the body of POST /api/dispatch: what was asked for,
// and the island it was asked at, or none for the world.
type dispatchRequest struct {
	Prompt string `json:"prompt"`
	Host   string `json:"host,omitempty"`
}

// dispatchResult is where the work would go, and who was asked.
type dispatchResult struct {
	dispatch.Verdict
	// Repos counts the repositories in scope.
	Repos int `json:"repos"`
	// Asked are the islands that answered.
	Asked []string `json:"asked"`
	// LeftOut are islands in scope with repositories that weren't asked,
	// because they were unreachable, or that didn't answer in time.
	LeftOut []leftOut `json:"leftOut"`
	// NamedIsland is the island the prompt named at the world, which the
	// work was placed on.
	NamedIsland string `json:"namedIsland,omitempty"`
}

// leftOut is an island whose repositories weren't considered.
type leftOut struct {
	Host string `json:"host"`
	// Why is "unreachable", "no answer", or what the island said.
	Why string `json:"why"`
}

// Why an island was left out.
const (
	leftUnreachable = "unreachable"
	leftNoAnswer    = "no answer"
)

// dispatchWork says where work asked for at the world or an island would
// go. The prompt's text goes to every island in scope that can be reached,
// to be scored there.
func (c *citadel) dispatchWork(w http.ResponseWriter, r *http.Request) {
	var req dispatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return
	}
	req.Prompt, req.Host = strings.TrimSpace(req.Prompt), strings.TrimSpace(req.Host)
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "a prompt is required")
		return
	}
	if req.Host != "" {
		if _, ok := c.islands.Get(req.Host); !ok {
			writeError(w, http.StatusNotFound, "no island named "+req.Host)
			return
		}
	}
	writeJSON(w, http.StatusOK, c.place(r.Context(), req))
}

// place decides where the work goes.
func (c *citadel) place(ctx context.Context, req dispatchRequest) dispatchResult {
	prompt := dispatch.ParsePrompt(req.Prompt)
	reachable := map[string]bool{}
	var hosts []string
	for _, isl := range c.islands.List() {
		reachable[isl.Host] = isl.Reachable
		hosts = append(hosts, isl.Host)
	}
	res := dispatchResult{Asked: []string{}, LeftOut: []leftOut{}}
	// At an island, that island is the scope, whatever the prompt names.
	// At the world, an island the prompt names is.
	scope := hosts
	if req.Host != "" {
		scope = []string{req.Host}
	} else if named := slices.DeleteFunc(slices.Clone(hosts), func(h string) bool { return !prompt.Names(h) }); len(named) == 1 {
		scope, res.NamedIsland = named, named[0]
	} else if len(named) > 1 {
		scope = named
	}
	repos := map[string][]projections.Repo{}
	for _, r := range c.terrain.List() {
		if slices.Contains(scope, r.Host) {
			repos[r.Host] = append(repos[r.Host], r)
			res.Repos++
		}
	}
	var ask []string
	for _, h := range scope {
		switch {
		case len(repos[h]) == 0:
		case !reachable[h]:
			res.LeftOut = append(res.LeftOut, leftOut{Host: h, Why: leftUnreachable})
		default:
			ask = append(ask, h)
		}
	}

	// One island with one repository has nothing to choose between, so the
	// work goes there without asking, whatever the prompt says.
	if len(scope) == 1 && len(ask) == 1 && len(repos[ask[0]]) == 1 {
		r := repos[ask[0]][0]
		only := dispatch.Candidate{Host: r.Host, MatchCandidate: v1.MatchCandidate{RepoID: r.ID, Name: r.Name, Reasons: []string{"the only repository on " + r.Host}}}
		res.Verdict = dispatch.Verdict{Do: dispatch.Start, Why: dispatch.WhyOnly, Target: &only, Ranked: []dispatch.Candidate{only}}
		return res
	}

	// A repository the prompt names settles it, without asking: the one
	// named, or a choice among those named, where the islands that hold
	// them can be reached.
	var named, namedAway []dispatch.Answer
	for _, h := range scope {
		var found []v1.MatchCandidate
		for _, r := range repos[h] {
			if prompt.Names(r.Name) {
				found = append(found, v1.MatchCandidate{RepoID: r.ID, Name: r.Name, Confidence: 1, Named: true, Reasons: []string{"the prompt names it"}})
			}
		}
		switch {
		case len(found) == 0:
		case slices.Contains(ask, h):
			named = append(named, dispatch.Answer{Host: h, Candidates: found})
		default:
			namedAway = append(namedAway, dispatch.Answer{Host: h, Candidates: found})
		}
	}
	if len(named) > 0 {
		for _, a := range named {
			res.Asked = append(res.Asked, a.Host)
		}
		res.Verdict = dispatch.Decide(named)
		return res
	}
	if len(namedAway) > 0 {
		// Every repository named is on an island that can't be reached, so
		// a person chooses, knowing that.
		res.Verdict = dispatch.Decide(namedAway)
		res.Do, res.Target, res.Host = dispatch.AskPerson, nil, ""
		res.Among = res.Ranked
		return res
	}

	// NATS request-reply takes only the first answer, so each island gets
	// a request of its own, all at once.
	var mu sync.Mutex
	var wg sync.WaitGroup
	var answers []dispatch.Answer
	for _, h := range ask {
		wg.Go(func() {
			data, err := c.requestWithin(ctx, v1.IslandFetchSubject(h, v1.FetchMatch), v1.MatchRequest{Prompt: req.Prompt}, v1.MatchTimeout)
			var m v1.MatchResult
			if err == nil {
				err = json.Unmarshal(data, &m)
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, nats.ErrNoResponders), errors.Is(err, nats.ErrTimeout):
				res.LeftOut = append(res.LeftOut, leftOut{Host: h, Why: leftNoAnswer})
			case err != nil:
				res.LeftOut = append(res.LeftOut, leftOut{Host: h, Why: err.Error()})
			default:
				answers = append(answers, dispatch.Answer{Host: h, Candidates: m.Candidates})
				res.Asked = append(res.Asked, h)
			}
		})
	}
	wg.Wait()
	slices.Sort(res.Asked)
	slices.SortFunc(res.LeftOut, func(a, b leftOut) int { return strings.Compare(a.Host, b.Host) })
	res.Verdict = dispatch.Decide(answers)
	return res
}

// pickRequest is the body of POST /api/dispatch/pick: the work, and the
// repositories on one island its agent chooses between.
type pickRequest struct {
	Prompt    string   `json:"prompt"`
	Host      string   `json:"host"`
	Among     []string `json:"among"`
	Agent     string   `json:"agent,omitempty"`
	SessionID string   `json:"sessionId,omitempty"`
}

// pickRepo asks an island's agent which of its repositories that scored
// close the work is for. The island describes only its own repositories
// to its own agent.
func (c *citadel) pickRepo(w http.ResponseWriter, r *http.Request) {
	var req pickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body: "+err.Error())
		return
	}
	req.Prompt, req.Host = strings.TrimSpace(req.Prompt), strings.TrimSpace(req.Host)
	if req.Prompt == "" || req.Host == "" || len(req.Among) < 2 {
		writeError(w, http.StatusBadRequest, "prompt, host, and two repositories or more are required")
		return
	}
	if req.SessionID != "" && !sessionIDPattern.MatchString(req.SessionID) {
		writeError(w, http.StatusBadRequest, "a session id is 8 to 64 letters and digits")
		return
	}
	if isl, ok := c.islands.Get(req.Host); !ok {
		writeError(w, http.StatusNotFound, "no island named "+req.Host)
		return
	} else if !isl.Reachable {
		writeError(w, http.StatusBadGateway, req.Host+" is unreachable right now")
		return
	}
	data, err := c.requestWithin(r.Context(), v1.IslandFetchSubject(req.Host, v1.FetchPick), v1.PickRequest{Prompt: req.Prompt, Among: req.Among, Agent: req.Agent, SessionID: req.SessionID}, v1.PickTimeout)
	writeFetched(w, req.Host, data, err)
}
