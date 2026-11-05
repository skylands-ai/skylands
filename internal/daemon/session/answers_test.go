package session

import (
	"encoding/json"
	"testing"

	v1 "github.com/skylands-ai/skylands/api/v1"
)

func TestCheckAnswers(t *testing.T) {
	fields := []v1.QuestionField{
		{Key: "file", Title: "File", Type: "string", Options: []v1.QuestionOption{{Value: "a.go"}, {Value: "b.go"}}, Required: true},
		{Key: "files", Type: "array", Options: []v1.QuestionOption{{Value: "a.go"}, {Value: "b.go"}}},
		{Key: "note", Type: "string"},
		{Key: "count", Type: "integer"},
		{Key: "ratio", Type: "number"},
		{Key: "sure", Type: "boolean"},
	}
	ok := map[string]string{
		"the required field":         `{"file":"a.go"}`,
		"every field":                `{"sure":false,"ratio":0.5,"count":3,"note":"x","files":["b.go","a.go"],"file":"b.go"}`,
		"a whole number written 3.0": `{"file":"a.go","count":3.0}`,
		"2^53":                       `{"file":"a.go","count":9007199254740992}`,
		"space around":               " {\"file\" : \"a.go\"} ",
	}
	for name, in := range ok {
		out, err := CheckAnswers(fields, json.RawMessage(in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !json.Valid(out) || out[0] != '{' {
			t.Errorf("%s: answers = %s", name, out)
		}
	}
	if out, _ := CheckAnswers(fields, json.RawMessage(`{"note":"x","file":"a.go","count":3.0}`)); string(out) != `{"count":3,"file":"a.go","note":"x"}` {
		t.Errorf("answers aren't canonical: %s", out)
	}
	refused := map[string]string{
		"nothing":                  ``,
		"not an object":            `["a.go"]`,
		"null":                     `null`,
		"a missing required field": `{"note":"x"}`,
		"an unknown field":         `{"file":"a.go","other":"x"}`,
		"a choice not offered":     `{"file":"c.go"}`,
		"a choice as a number":     `{"file":1}`,
		"choices not a list":       `{"file":"a.go","files":"a.go"}`,
		"a choice twice":           `{"file":"a.go","files":["a.go","a.go"]}`,
		"no choices":               `{"file":"a.go","files":[]}`,
		"empty text":               `{"file":"a.go","note":""}`,
		"a null value":             `{"file":"a.go","note":null}`,
		"text as a number":         `{"file":"a.go","note":3}`,
		"a fraction for integer":   `{"file":"a.go","count":1.5}`,
		"an integer past 2^53":     `{"file":"a.go","count":9007199254740993e3}`,
		"a number as text":         `{"file":"a.go","ratio":"0.5"}`,
		"a boolean as text":        `{"file":"a.go","sure":"true"}`,
		"a key twice":              `{"file":"a.go","file":"b.go"}`,
		"invalid JSON":             `{"file":`,
	}
	for name, in := range refused {
		if out, err := CheckAnswers(fields, json.RawMessage(in)); err == nil {
			t.Errorf("%s: accepted as %s", name, out)
		}
	}
}

func TestPickOptionNeverChoosesAlways(t *testing.T) {
	options := []v1.PermissionOption{
		{ID: "always", Kind: "allow_always"},
		{ID: "once", Kind: "allow_once"},
		{ID: "never", Kind: "reject_always"},
		{ID: "no", Kind: "reject_once"},
	}
	cases := []struct {
		allow  bool
		wanted string
		want   string
	}{
		{true, "", "once"},
		{false, "", "no"},
		{true, "once", "once"},
		{true, "always", ""},
		{false, "never", ""},
		{true, "no", ""},
	}
	for _, c := range cases {
		if got := pickOption(options, c.allow, c.wanted); got != c.want {
			t.Errorf("pickOption(allow %v, %q) = %q, want %q", c.allow, c.wanted, got, c.want)
		}
	}
	if got := pickOption(options[:1], true, ""); got != "" {
		t.Errorf("with only an always option, pickOption = %q, want none", got)
	}
}
