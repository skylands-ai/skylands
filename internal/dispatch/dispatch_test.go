package dispatch

import (
	"slices"
	"strings"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func TestStem(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"pricing", "pric"}, {"prices", "pric"}, {"price", "pric"}, {"priced", "pric"},
		{"currencies", "currency"}, {"currency", "currency"},
		{"caches", "cach"}, {"cache", "cach"},
		{"status", "status"}, {"process", "process"}, {"go", "go"},
		{"strings", "str"}, {"string", "str"},
	} {
		if got := stem(tc.in); got != tc.want {
			t.Errorf("stem(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParts(t *testing.T) {
	if got := parts("internal/PricingEngine.ts"); !slices.Equal(got, []string{"internal", "pricing", "engine", "ts"}) {
		t.Errorf("parts = %q", got)
	}
}

func TestParsePromptKeepsWhatSaysWhere(t *testing.T) {
	p := ParsePrompt("Add multi-currency support to pricing, and fix the tests for pricing")
	var got []string
	for _, w := range p.words {
		got = append(got, strings.Join(w.parts, "-"))
	}
	// "add", "support", "fix", and "tests" say nothing about where; the
	// compound is one word, and a word said twice counts once.
	if !slices.Equal(got, []string{"multi-currency", "pricing"}) {
		t.Errorf("words = %q", got)
	}
	if !ParsePrompt("fix the tests").Empty() {
		t.Error("a prompt of skipped words has something to score")
	}
}

func TestNames(t *testing.T) {
	for _, tc := range []struct {
		prompt, name string
		want         bool
	}{
		{"add a coupon to storefront", "storefront", true},
		{"add a coupon to Storefront.", "storefront", true},
		{"round prices in storefront-pricing", "storefront-pricing", true},
		{"round prices in the storefront pricing crate", "storefront-pricing", true},
		{"round prices in pricing", "storefront-pricing", false},
		{"storefronts everywhere", "storefront", false},
		// A name made of skipped words, or too short, names nothing.
		{"add a test for login", "test", false},
		{"move it to go", "go", false},
	} {
		if got := ParsePrompt(tc.prompt).Names(tc.name); got != tc.want {
			t.Errorf("%q names %q = %v, want %v", tc.prompt, tc.name, got, tc.want)
		}
	}
}

// storefront and catalog are two repositories on one island.
func storefront() *Entry {
	return Build(Source{
		ID: "sf", Name: "storefront",
		Modules: []Module{{Name: "storefront-pricing", Manifest: "crates/pricing/Cargo.toml"}},
		Paths:   []string{"crates/pricing/src/engine.rs", "crates/pricing/Cargo.toml", "crates/cart/src/lib.rs", "internal/pricing/rounding.go", "README.md", "vendor/github.com/auth/authentication.go"},
		Readme:  "# Storefront\n\nA shop with [![CI](https://ci.example/badge.svg)](https://ci.example) carts and prices in any currency.",
		Commits: []string{"Round prices to the cent", "Add the cart"},
	})
}

func catalog() *Entry {
	return Build(Source{
		ID: "cat", Name: "catalog",
		Paths:   []string{"src/products/list.ts", "src/search/index.ts", "src/authentication/login.ts", "README.md"},
		Readme:  "# Catalog\n\nProducts and search.",
		Commits: []string{"Show the pricing tier on products"},
	})
}

func TestScoreSaysHowMuchOfThePromptARepositoryAccountsFor(t *testing.T) {
	p := ParsePrompt("add multi-currency support to pricing")
	sf := storefront().Score(p, nil, "")
	cat := catalog().Score(p, nil, "")
	// pricing names a module of storefront's, at full weight, and currency
	// is in its README: (1 + 0.4) / 2.
	if sf.Confidence != 0.7 || sf.Named {
		t.Errorf("storefront = %+v", sf)
	}
	if !slices.Contains(sf.Reasons, "pricing in the module storefront-pricing") || !slices.Contains(sf.Reasons, "currency in the README") {
		t.Errorf("storefront's reasons = %q", sf.Reasons)
	}
	// catalog has pricing only in a commit subject.
	if cat.Confidence != 0.2 || !slices.Contains(cat.Reasons, "pricing in a commit, “Show the pricing tier on products”") {
		t.Errorf("catalog = %+v", cat)
	}
	got := Rank([]*Entry{catalog(), storefront()}, p, nil, "")
	if len(got) != 2 || got[0].RepoID != "sf" {
		t.Errorf("Rank = %+v", got)
	}
}

func TestScorePrefersTheDirectoryAPathNames(t *testing.T) {
	c := storefront().Score(ParsePrompt("the rounding is off"), nil, "")
	if c.Confidence != 0.7 || !slices.Equal(c.Reasons, []string{"rounding in internal/pricing/rounding.go"}) {
		t.Errorf("rounding = %+v", c)
	}
	c = storefront().Score(ParsePrompt("cart totals"), nil, "")
	// cart is a directory under crates, so the reason names the directory;
	// totals matches nothing.
	if !slices.Contains(c.Reasons, "cart in crates/cart/") || c.Confidence != 0.35 {
		t.Errorf("cart = %+v", c)
	}
}

func TestScoreLeavesOutVendoredCodeAndFileExtensions(t *testing.T) {
	if c := storefront().Score(ParsePrompt("authentication"), nil, ""); c.Confidence != 0 {
		t.Errorf("vendored code matched: %+v", c)
	}
	if c := storefront().Score(ParsePrompt("rewrite it in rs"), nil, ""); c.Confidence != 0 {
		t.Errorf("an extension matched: %+v", c)
	}
}

func TestScoreMatchesTheStartOfAWord(t *testing.T) {
	c := catalog().Score(ParsePrompt("auth is broken"), nil, "")
	// auth begins authentication, a directory: 0.7 * 0.75, over two words.
	if c.Confidence != 0.26 || !slices.Contains(c.Reasons, "auth in src/authentication/") {
		t.Errorf("auth = %+v", c)
	}
}

func TestScoreNamedSettlesIt(t *testing.T) {
	c := storefront().Score(ParsePrompt("in storefront-pricing, round half up"), nil, "")
	if !c.Named || c.Confidence != 1 || !strings.Contains(c.Reasons[0], "storefront-pricing") {
		t.Errorf("named module = %+v", c)
	}
	if c := catalog().Score(ParsePrompt("catalog: list products by date"), nil, ""); !c.Named {
		t.Errorf("named repository = %+v", c)
	}
}

func TestScoreReadsTitlesExceptTheOneSkipped(t *testing.T) {
	titles := []Title{{Session: "s1", Title: "Speed up checkout"}}
	p := ParsePrompt("checkout is slow")
	if c := catalog().Score(p, titles, ""); !slices.Contains(c.Reasons, "checkout in the conversation “Speed up checkout”") {
		t.Errorf("with the title = %+v", c)
	}
	if c := catalog().Score(p, titles, "s1"); c.Confidence != 0 {
		t.Errorf("with the title skipped = %+v", c)
	}
}

func cand(id string, conf float64) v1.MatchCandidate {
	return v1.MatchCandidate{RepoID: id, Name: id, Confidence: conf}
}

func TestDecide(t *testing.T) {
	named := v1.MatchCandidate{RepoID: "sf", Name: "storefront", Confidence: 1, Named: true}
	for _, tc := range []struct {
		name    string
		answers []Answer
		do, why string
		target  string
		host    string
		among   []string
	}{
		{name: "a clear winner", answers: []Answer{{"ws", []v1.MatchCandidate{cand("sf", 0.7), cand("cat", 0.2)}}, {"laptop", []v1.MatchCandidate{cand("docs", 0.3)}}},
			do: Start, why: WhyClear, target: "sf"},
		{name: "named", answers: []Answer{{"ws", []v1.MatchCandidate{cand("cat", 0.9), named}}},
			do: Start, why: WhyNamed, target: "sf"},
		{name: "named on two islands", answers: []Answer{{"ws", []v1.MatchCandidate{named}}, {"laptop", []v1.MatchCandidate{named}}},
			do: AskPerson, why: WhyNamed, among: []string{"laptop/sf", "ws/sf"}},
		{name: "close on one island", answers: []Answer{{"ws", []v1.MatchCandidate{cand("sf", 0.5), cand("cat", 0.4), cand("web", 0.3)}}, {"laptop", []v1.MatchCandidate{cand("docs", 0.1)}}},
			do: AskAgent, why: WhyClose, host: "ws", among: []string{"ws/sf", "ws/cat"}},
		{name: "close across islands", answers: []Answer{{"ws", []v1.MatchCandidate{cand("sf", 0.5)}}, {"laptop", []v1.MatchCandidate{cand("docs", 0.45)}}},
			do: AskPerson, why: WhyClose, among: []string{"ws/sf", "laptop/docs"}},
		{name: "one repository on two islands", answers: []Answer{{"ws", []v1.MatchCandidate{cand("sf", 0.7)}}, {"laptop", []v1.MatchCandidate{cand("sf", 0.7)}}},
			do: AskPerson, why: WhyClose, among: []string{"laptop/sf", "ws/sf"}},
		{name: "a lead of exactly Lead is close", answers: []Answer{{"ws", []v1.MatchCandidate{cand("sf", 0.55), cand("cat", 0.4)}}},
			do: AskAgent, why: WhyClose, host: "ws", among: []string{"ws/sf", "ws/cat"}},
		{name: "a lone weak fit", answers: []Answer{{"ws", []v1.MatchCandidate{cand("sf", 0.3), cand("cat", 0.1)}}},
			do: AskPerson, why: WhyWeak, among: []string{"ws/sf"}},
		{name: "nothing fits", answers: []Answer{{"ws", []v1.MatchCandidate{cand("sf", 0.1)}}, {"laptop", nil}},
			do: AskPerson, why: WhyNothing},
		{name: "no answers", do: AskPerson, why: WhyNothing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := Decide(tc.answers)
			var among []string
			for _, c := range v.Among {
				among = append(among, c.Host+"/"+c.RepoID)
			}
			target := ""
			if v.Target != nil {
				target = v.Target.RepoID
			}
			if v.Do != tc.do || v.Why != tc.why || target != tc.target || v.Host != tc.host || !slices.Equal(among, tc.among) {
				t.Errorf("Decide = %s/%s target %q host %q among %q, want %s/%s target %q host %q among %q", v.Do, v.Why, target, v.Host, among, tc.do, tc.why, tc.target, tc.host, tc.among)
			}
			if v.Ranked == nil {
				t.Error("Ranked is nil")
			}
		})
	}
}

func TestPick(t *testing.T) {
	q := PickQuestion("add a coupon to the cart", []string{storefront().Describe([]string{"cart in crates/cart/"}), catalog().Describe(nil)})
	if !IsPickQuestion(q) || IsPickQuestion("add a coupon") {
		t.Error("IsPickQuestion doesn't tell a pick question apart")
	}
	for _, want := range []string{"1. storefront\n", "Modules: storefront-pricing", "Top level: README.md crates/ internal/\n", "README: Storefront A shop with carts and prices in any currency.\n", "Matched: cart in crates/cart/", "2. catalog\n"} {
		if !strings.Contains(q, want) {
			t.Errorf("the question lacks %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, "vendor") {
		t.Errorf("the question describes vendored code:\n%s", q)
	}
	for _, tc := range []struct {
		reply string
		want  int
	}{
		{"2", 2}, {" 1.\n", 1}, {"**2**", 2}, {"2 – catalog", 2}, {"0", 0}, {"3", 0}, {"unsure", 0}, {"2 of them could be", 0},
	} {
		if got := ParsePick(tc.reply, 2); got != tc.want {
			t.Errorf("ParsePick(%q) = %d, want %d", tc.reply, got, tc.want)
		}
	}
	if got := FakePick(PickQuestion("list products in the catalog", []string{"storefront", "catalog"})); got != "2" {
		t.Errorf("FakePick = %q, want 2", got)
	}
	if got := FakePick(PickQuestion("speed it up", []string{"storefront", "catalog"})); got != "0" {
		t.Errorf("FakePick of neither = %q, want 0", got)
	}
}
