package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
)

// diffContext is how many unchanged lines a diff shows around a change.
const diffContext = 3

// printDecision prints one decision whole: who answered and how, its
// signature, the input the request named, and what the tool call
// produced, each checked against the hash the session's events name. It
// returns an error, after printing, when a kept input or output doesn't
// match its hash.
func printDecision(w io.Writer, d socket.DecisionDetail) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	row := func(k, v string) { fmt.Fprintf(tw, "%s\t%s\n", k, v) }
	row("Request", d.Request)
	row("Conversation", fmt.Sprintf("%s (session %s), %s on %s", d.Conversation, d.Session, d.Agent, d.Repo))
	tool := d.Tool
	if d.Kind != "" {
		tool += " (" + d.Kind + ")"
	}
	row("Tool", tool)
	if d.ToolCall != "" {
		row("Tool call", d.ToolCall)
	}
	row("Asked", d.Asked.Local().Format("2006-01-02 15:04:05"))
	answer := d.Answer
	if d.Option != "" {
		answer += " with option " + d.Option
	}
	if d.Reason != "" {
		answer += " (" + d.Reason + ")"
	}
	row("Answer", answer+", "+d.At.Local().Format("2006-01-02 15:04:05"))
	by := decidedBy(d.DecisionInfo)
	if d.By == "citadel" {
		by += ", through the citadel"
	}
	row("By", by)
	if d.Approver != "" {
		row("Approver", d.Approver)
	}
	if d.World != "" {
		row("World", d.World)
	}
	if d.Signature != "" {
		row("Signature", d.Signature)
	}
	if d.Sig != "" {
		row("", d.Sig)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	var problems []string
	fmt.Fprintln(w)
	switch {
	case !d.InputKept:
		fmt.Fprintf(w, "Input: %d bytes, SHA-256 %s, not kept on this host.\n", d.InputSize, d.InputHash)
	case checks(d.Input, d.InputHash, d.InputSize):
		fmt.Fprintf(w, "Input: %d bytes, SHA-256 %s, matches the request.\n", len(d.Input), d.InputHash)
		printInput(w, d.Input)
	default:
		fmt.Fprintf(w, "Input: %d bytes kept, SHA-256 %s, which DOESN'T MATCH the request's %s.\n", len(d.Input), hashOf(d.Input), d.InputHash)
		printInput(w, d.Input)
		problems = append(problems, "the kept input doesn't match the hash the request names")
	}

	fmt.Fprintln(w)
	switch {
	case d.CallStatus == "":
		fmt.Fprintln(w, "Output: the tool call hasn't finished, or the agent never said it did.")
	case d.OutputHash == "":
		fmt.Fprintf(w, "Output: %s, with nothing kept on this host.\n", d.CallStatus)
	case !d.OutputKept:
		fmt.Fprintf(w, "Output: %s, %d bytes, SHA-256 %s, not kept on this host.\n", d.CallStatus, d.OutputSize, d.OutputHash)
	case checks(d.Output, d.OutputHash, d.OutputSize):
		fmt.Fprintf(w, "Output: %s, %d bytes, SHA-256 %s, matches the tool call.\n", d.CallStatus, len(d.Output), d.OutputHash)
		printOutput(w, d.Output)
	default:
		fmt.Fprintf(w, "Output: %s, %d bytes kept, SHA-256 %s, which DOESN'T MATCH the tool call's %s.\n", d.CallStatus, len(d.Output), hashOf(d.Output), d.OutputHash)
		printOutput(w, d.Output)
		problems = append(problems, "the kept output doesn't match the hash the tool call names")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// checks reports whether data is what a hash and a size name.
func checks(data []byte, hash string, size int) bool {
	return hashOf(data) == hash && len(data) == size
}

// printInput prints an input for reading. An object prints field by
// field, in key order, with a text of several lines, such as a file's new
// content, as a block of its own; other JSON prints indented. The hash is
// over the bytes as kept, not as printed.
func printInput(w io.Writer, input []byte) {
	if len(input) == 0 {
		fmt.Fprintln(w, "  (empty)")
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) == nil && fields != nil {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			var text string
			if json.Unmarshal(fields[k], &text) == nil && strings.Contains(text, "\n") {
				fmt.Fprintf(w, "  %s:\n", k)
				for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
					fmt.Fprintf(w, "    %s\n", line)
				}
				continue
			}
			var buf bytes.Buffer
			if json.Indent(&buf, fields[k], "  ", "  ") != nil {
				buf.Reset()
				buf.Write(fields[k])
			}
			fmt.Fprintf(w, "  %s: %s\n", k, buf.String())
		}
		return
	}
	var buf bytes.Buffer
	if json.Indent(&buf, input, "  ", "  ") == nil {
		fmt.Fprintf(w, "  %s\n", buf.String())
		return
	}
	printBlock(w, string(input))
}

// printOutput prints what a tool call produced: each piece of content in
// turn, a diff as the lines it changed, and the raw output last.
func printOutput(w io.Writer, data []byte) {
	var out v1.ToolCallOutput
	if err := json.Unmarshal(data, &out); err != nil {
		fmt.Fprintf(w, "  (not a tool call's output: %v)\n", err)
		printBlock(w, string(data))
		return
	}
	if len(out.Content) == 0 && out.Output == "" {
		fmt.Fprintln(w, "  (nothing)")
		return
	}
	for _, c := range out.Content {
		switch c.Type {
		case "text":
			printBlock(w, c.Text)
		case "diff":
			fmt.Fprintf(w, "  diff %s\n", c.Path)
			old := ""
			if c.OldText != nil {
				old = *c.OldText
			} else {
				fmt.Fprintln(w, "  (a new file)")
			}
			for _, line := range diffLines(old, c.NewText) {
				fmt.Fprintf(w, "  %s\n", line)
			}
		case "terminal":
			fmt.Fprintf(w, "  (terminal %s: its output stayed in the agent's terminal)\n", c.TerminalID)
		default:
			fmt.Fprintf(w, "  (%s) %s\n", c.Type, c.Text)
		}
	}
	if out.Output != "" {
		fmt.Fprintln(w, "  raw output:")
		printBlock(w, out.Output)
	}
}

// printBlock prints text indented by two spaces.
func printBlock(w io.Writer, text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
}

// diffLines shows how new differs from old as one change: the lines
// both start and end with are left out but for a few around the change,
// the old lines between them marked "-" and the new ones "+".
func diffLines(old, new string) []string {
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	}
	a, b := split(old), split(new)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	post := 0
	for post < len(a)-pre && post < len(b)-pre && a[len(a)-1-post] == b[len(b)-1-post] {
		post++
	}
	if pre == len(a) && pre == len(b) {
		return []string{"(no change)"}
	}
	var out []string
	from := max(pre-diffContext, 0)
	if from > 0 {
		out = append(out, fmt.Sprintf("@@ line %d @@", from+1))
	}
	for _, l := range a[from:pre] {
		out = append(out, "  "+l)
	}
	for _, l := range a[pre : len(a)-post] {
		out = append(out, "- "+l)
	}
	for _, l := range b[pre : len(b)-post] {
		out = append(out, "+ "+l)
	}
	for _, l := range a[len(a)-post : min(len(a)-post+diffContext, len(a))] {
		out = append(out, "  "+l)
	}
	return out
}
