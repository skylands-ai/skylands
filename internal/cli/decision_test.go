package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
)

func TestPrintDecision(t *testing.T) {
	input := []byte(`{"path":"a.go","content":"x"}`)
	old := "one\ntwo\nthree\nfour\nfive\nsix\n"
	output, _ := json.Marshal(v1.ToolCallOutput{
		Content: []v1.ToolCallContent{{Type: "text", Text: "wrote a.go"}, {Type: "diff", Path: "a.go", OldText: &old, NewText: "one\ntwo\nthree\nFOUR\nfive\nsix\n"}},
		Output:  "exit 0",
	})
	d := socket.DecisionDetail{
		DecisionInfo: socket.DecisionInfo{Request: "r1", Session: "s1", Conversation: "Fix it", Repo: "repo", Agent: "claude", Tool: "Edit a.go", Kind: "edit",
			Answer: "allowed", Option: "allow_once", By: "citadel", Approver: "UABC", ApproverName: "Laptop", Signature: "verified", Asked: time.Now(), At: time.Now()},
		Sig: "c2ln", World: "OWORLD", ToolCall: "t1",
		InputHash: hashOf(input), InputSize: len(input), Input: input, InputKept: true,
		CallStatus: "completed", OutputHash: hashOf(output), OutputSize: len(output), Output: output, OutputKept: true,
	}
	var out bytes.Buffer
	if err := printDecision(&out, d); err != nil {
		t.Fatalf("printDecision = %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{
		"Laptop, through the citadel", "UABC", "c2ln", "allowed with option allow_once",
		fmt.Sprintf("Input: %d bytes, SHA-256 %s, matches the request.", len(input), d.InputHash), "  path: \"a.go\"",
		"Output: completed", "matches the tool call.", "wrote a.go", "diff a.go", "- four", "+ FOUR", "  three", "raw output:", "exit 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the decision lacks %q:\n%s", want, text)
		}
	}

	// Bytes that don't match their hashes are shown, said not to match, and
	// fail the command.
	changed := d
	changed.Input = []byte(`{"path":"b.go","content":"x"}`)
	changed.Output = append([]byte(nil), output...)
	changed.Output[len(changed.Output)-2] = 'X'
	out.Reset()
	err := printDecision(&out, changed)
	if err == nil || !strings.Contains(err.Error(), "input") || !strings.Contains(err.Error(), "output") {
		t.Errorf("printDecision of changed bytes = %v", err)
	}
	if !strings.Contains(out.String(), "DOESN'T MATCH the request's") || !strings.Contains(out.String(), "DOESN'T MATCH the tool call's") {
		t.Errorf("the mismatch isn't said:\n%s", out.String())
	}

	// Nothing kept, or a call not finished, is said plainly.
	bare := d
	bare.Input, bare.InputKept, bare.CallStatus, bare.Output, bare.OutputKept, bare.OutputHash = nil, false, "", nil, false, ""
	out.Reset()
	if err := printDecision(&out, bare); err != nil || !strings.Contains(out.String(), "not kept on this host") || !strings.Contains(out.String(), "hasn't finished") {
		t.Errorf("printDecision with nothing kept = %v\n%s", err, out.String())
	}
}

func TestPrintInput(t *testing.T) {
	var out bytes.Buffer
	printInput(&out, []byte(`{"path":"a.go","content":"line one\nline two\n","n":3,"opts":{"x":true}}`))
	want := "  content:\n    line one\n    line two\n  n: 3\n  opts: {\n    \"x\": true\n  }\n  path: \"a.go\"\n"
	if out.String() != want {
		t.Errorf("printInput =\n%s\nwant\n%s", out.String(), want)
	}
	out.Reset()
	printInput(&out, []byte(`not json`))
	if out.String() != "  not json\n" {
		t.Errorf("printInput of text = %q", out.String())
	}
}

func TestDiffLines(t *testing.T) {
	cases := []struct {
		old, new string
		want     []string
	}{
		{"a\nb\n", "a\nb\n", []string{"(no change)"}},
		{"", "x\ny\n", []string{"+ x", "+ y"}},
		{"a\nb\nc\n", "a\nc\n", []string{"  a", "- b", "  c"}},
		{"1\n2\n3\n4\n5\n6\n7\n", "1\n2\n3\n4\n5\nsix\n7\n", []string{"@@ line 3 @@", "  3", "  4", "  5", "- 6", "+ six", "  7"}},
	}
	for _, c := range cases {
		if got := diffLines(c.old, c.new); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("diffLines(%q, %q) = %q, want %q", c.old, c.new, got, c.want)
		}
	}
}
