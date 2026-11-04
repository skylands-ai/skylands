package cli

// The local CLI talks to the daemon on this host over its Unix socket. It
// registers repos, lists what is waiting on a person, approves or denies
// those requests, lists, stops, and cleans up sessions, and reports status.
// The daemon accepts these from the OS user that owns it, which unblocks
// agents even while the host is offline.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/rules"
)

// socketFlag adds the --socket flag shared by every local command.
func socketFlag(cmd *cobra.Command, socketPath *string) {
	cmd.Flags().StringVar(socketPath, "socket", defaultSocketPath(), "Unix socket of the daemon on this host")
}

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "repo",
		GroupID: groupLocal,
		Short:   "Manage the repositories the daemon on this host works in",
	}
	cmd.AddCommand(newRepoRegisterCmd())
	return cmd
}

func newRepoRegisterCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "register <path>",
		Short: "Register a repository as a terrace on this island",
		Long: `Repo register tells the daemon about a repository on this host. The daemon
snapshots its tracked files and their sizes, and a terrace with a building
for every file appears on the island.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := socket.Dial(socketPath).RegisterRepo(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Registered %s: %d tracked files at %s\n", repo.Name, repo.Files, repo.Path)
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "request",
		GroupID: groupLocal,
		Short:   "List, approve, and deny requests waiting on a person on this host",
	}
	cmd.AddCommand(newRequestListCmd(), newRequestHistoryCmd(), newRequestApproveCmd(), newRequestDenyCmd(), newRequestAnswerCmd())
	return cmd
}

func newRequestListCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List what is waiting on a person on this host",
		Long: `Request list shows every tool call an agent on this host will not make
without an answer: its id, the conversation, the agent, the tool, and how
long it has waited. Approve or deny one by its id.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cli := socket.Dial(socketPath)
			reqs, err := cli.Requests(cmd.Context())
			if err != nil {
				return err
			}
			questions, err := cli.Questions(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(reqs) == 0 && len(questions) == 0 {
				fmt.Fprintln(out, "Nothing is waiting on you.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			if len(reqs) > 0 {
				fmt.Fprintln(tw, "REQUEST\tCONVERSATION\tAGENT\tREPO\tTOOL\tWAITING")
				for _, r := range reqs {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, cut(r.Conversation, 40), r.Agent, r.Repo, cut(r.Tool, 50), time.Since(r.Since).Truncate(time.Second))
				}
			}
			if len(questions) > 0 {
				if len(reqs) > 0 {
					fmt.Fprintln(tw)
				}
				fmt.Fprintln(tw, "QUESTION\tCONVERSATION\tAGENT\tREPO\tASKS\tWAITING")
				for _, q := range questions {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", q.ID, cut(q.Conversation, 40), q.Agent, q.Repo, cut(q.Message, 50), time.Since(q.Since).Truncate(time.Second))
					for _, f := range q.Fields {
						fmt.Fprintf(tw, "\t\t\t\t  %s\t\n", fieldText(f))
					}
				}
			}
			return tw.Flush()
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRequestHistoryCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "history [session]",
		Short: "List the decisions made on this host's requests",
		Long: `Request history lists every tool call that waited on a person on this host,
in one session or in all of them, oldest first: the tool, the answer, when,
and who gave it. A decision made through the citadel carries the signature
of the approver who made it, which is checked again here against the
world's approvers as they stand now. It reads from this host's daemon, so it
works while the citadel is unreachable.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			session := ""
			if len(args) == 1 {
				session = args[0]
			}
			history, err := socket.Dial(socketPath).History(cmd.Context(), session)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(history) == 0 {
				fmt.Fprintln(out, "No decisions yet.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "WHEN\tCONVERSATION\tTOOL\tANSWER\tBY\tSIGNATURE")
			for _, d := range history {
				answer := d.Answer
				if d.Reason != "" {
					answer += " (" + d.Reason + ")"
				}
				sig := d.Signature
				if sig == "" {
					sig = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.At.Local().Format("Jan 2 15:04:05"), cut(d.Conversation, 30), cut(d.Tool, 40), answer, decidedBy(d), sig)
			}
			return tw.Flush()
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

// decidedBy names who settled a request: an approver by name, the host's
// own CLI, a session's trust, or a rule.
func decidedBy(d socket.DecisionInfo) string {
	switch d.By {
	case "citadel":
		switch {
		case d.ApproverName != "":
			return d.ApproverName
		case d.Approver != "":
			return d.Approver[:10] + "\u2026"
		}
		return "a browser"
	case "local":
		return "this host's CLI"
	case "trust":
		return "trusted session"
	case "":
		return "-"
	}
	return d.By
}

func newRequestApproveCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "approve <request>",
		Short: "Approve a waiting request on this host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := socket.Dial(socketPath).Approve(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Approved %s for %s in %s\n", r.Tool, r.Agent, r.Repo)
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRequestDenyCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "deny <request>",
		Short: "Deny a waiting request on this host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := socket.Dial(socketPath).Deny(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Denied %s for %s in %s\n", r.Tool, r.Agent, r.Repo)
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "session",
		GroupID: groupLocal,
		Short:   "List, stop, and clean up agent sessions on this host",
	}
	cmd.AddCommand(newSessionListCmd(), newSessionCancelCmd(), newSessionStopCmd(), newSessionRemoveCmd(), newSessionPruneCmd())
	return cmd
}

func newSessionListCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every session on this host",
		Long: `Session list shows each session on this host: its id, the conversation,
the agent, the repository, its status, where it works, and what became of
its changes. Stop, remove, or prune sessions by id.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessions, err := socket.Dial(socketPath).Sessions(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(sessions) == 0 {
				fmt.Fprintln(out, "No sessions on this host.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SESSION\tCONVERSATION\tAGENT\tREPO\tSTATUS\tWHERE\tCHANGES")
			for _, s := range sessions {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, cut(conversationOf(s), 36), s.Agent, s.Repo, s.Status, whereOf(s), changesOf(s))
			}
			return tw.Flush()
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newSessionCancelCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "cancel <session>",
		Short: "Interrupt the turn in progress without ending the session",
		Long: `Session cancel tells the agent to stop what it is doing. The session stays
open and goes idle once the agent has stopped, so the next reply can steer
it. Anything the turn was waiting on a person for is withdrawn.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := socket.Dial(socketPath).CancelTurn(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Interrupted %s (%s). It goes idle once the agent stops; reply to steer it.\n", s.ID, cut(conversationOf(s), 50))
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newSessionStopCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "stop <session>",
		Short: "End a session, cancelling any turn in progress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := socket.Dial(socketPath).StopSession(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stopped %s (%s). Its worktree stays until removed.\n", s.ID, cut(conversationOf(s), 50))
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newSessionRemoveCmd() *cobra.Command {
	var socketPath string
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <session>",
		Short: "Delete a finished session's worktree and branch",
		Long: `Session remove deletes the worktree of a session that has ended, and its
branch. A worktree with uncommitted changes, or a branch with commits its
base lacks, is kept unless --force is given. Stop an open session first.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := socket.Dial(socketPath).RemoveSession(cmd.Context(), args[0], force)
			if err != nil {
				return err
			}
			s := res.Session
			switch {
			case res.BranchDeleted:
				fmt.Fprintf(cmd.OutOrStdout(), "Removed the worktree and branch %s of %s.\n", s.Branch, s.ID)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "Removed the worktree of %s. The branch %s is kept: %s lacks its commits.\n", s.ID, s.Branch, s.Base)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even with uncommitted changes or unmerged commits")
	socketFlag(cmd, &socketPath)
	return cmd
}

func newSessionPruneCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete what finished sessions left behind",
		Long: `Session prune removes the worktrees and branches of every session that has
ended, keeping any worktree with uncommitted changes and any branch with
commits its base lacks, and says which.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := socket.Dial(socketPath).PruneSessions(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(results) == 0 {
				fmt.Fprintln(out, "Nothing to prune.")
				return nil
			}
			for _, r := range results {
				switch {
				case r.Removed && r.BranchDeleted:
					fmt.Fprintf(out, "Removed the worktree and branch of %s (%s).\n", r.Session.ID, cut(conversationOf(r.Session), 40))
				case r.Removed:
					fmt.Fprintf(out, "Removed the worktree of %s (%s); kept the branch %s, which has unmerged commits.\n", r.Session.ID, cut(conversationOf(r.Session), 40), r.Session.Branch)
				default:
					fmt.Fprintf(out, "Kept %s (%s): %s\n", r.Session.ID, cut(conversationOf(r.Session), 40), r.Kept)
				}
			}
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rule",
		GroupID: groupLocal,
		Short:   "List, add, and remove the rules that settle tool calls in a repository's sessions",
		Long: `Rules settle what an agent may do before anyone is asked. A rule is a
decision, allow, ask, or deny; a tool call kind, read, edit, delete, move,
search, execute, think, fetch, other, or * for any; and an optional
pattern: a command pattern for execute such as "go test *", a URL pattern
for fetch, and a path pattern for the rest, where "worktree" means
everything inside the session's worktree. The most specific matching rule
wins, a deny beats an allow among equals, and no match asks a person. A
session copies its repository's rules when it starts.`,
	}
	cmd.AddCommand(newRuleListCmd(), newRuleAddCmd(), newRuleRemoveCmd())
	return cmd
}

func newRuleListCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every repository's rules on this host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repos, err := socket.Dial(socketPath).Rules(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(repos) == 0 {
				fmt.Fprintln(out, "No repositories are registered on this host.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "REPO\t#\tDECISION\tKIND\tPATTERN")
			for _, r := range repos {
				if len(r.Rules) == 0 {
					fmt.Fprintf(tw, "%s\t-\t(no rules; reads, searches, and thinking pass, the rest asks)\t\t\n", r.Repo)
				}
				for i, rule := range r.Rules {
					fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", r.Repo, i+1, rule.Decision, rule.Kind, rule.Pattern)
				}
			}
			return tw.Flush()
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRuleAddCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "add <repo> <allow|ask|deny> <kind> [pattern]",
		Short: "Add a rule to a repository on this host",
		Example: `  skylands rule add skylands allow edit worktree
  skylands rule add skylands allow execute "go test *"
  skylands rule add skylands deny execute "rm -rf *"`,
		Args: cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli := socket.Dial(socketPath)
			r, err := repoRulesNamed(cmd.Context(), cli, args[0])
			if err != nil {
				return err
			}
			rule := v1.Rule{Decision: args[1], Kind: args[2]}
			if len(args) == 4 {
				rule.Pattern = args[3]
			}
			info, err := cli.SetRepoRules(cmd.Context(), r.RepoID, append(r.Rules, rule))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Added rule %d to %s: %s. New sessions on it start with %d rules.\n", len(info.Rules), info.Repo, rules.Text(info.Rules[len(info.Rules)-1]), len(info.Rules))
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

func newRuleRemoveCmd() *cobra.Command {
	var socketPath string
	cmd := &cobra.Command{
		Use:   "remove <repo> <number>",
		Short: "Remove a rule from a repository on this host, by its number in rule list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli := socket.Dial(socketPath)
			r, err := repoRulesNamed(cmd.Context(), cli, args[0])
			if err != nil {
				return err
			}
			var n int
			if _, err := fmt.Sscan(args[1], &n); err != nil || n < 1 || n > len(r.Rules) {
				return fmt.Errorf("%s has %d rules; give a number from rule list", r.Repo, len(r.Rules))
			}
			removed := r.Rules[n-1]
			info, err := cli.SetRepoRules(cmd.Context(), r.RepoID, append(append([]v1.Rule{}, r.Rules[:n-1]...), r.Rules[n:]...))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed from %s: %s. %d rules remain.\n", info.Repo, rules.Text(removed), len(info.Rules))
			return nil
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
}

// repoRulesNamed finds a repository's rules by the repository's name or id.
func repoRulesNamed(ctx context.Context, cli *socket.Client, name string) (socket.RepoRulesInfo, error) {
	repos, err := cli.Rules(ctx)
	if err != nil {
		return socket.RepoRulesInfo{}, err
	}
	for _, r := range repos {
		if r.Repo == name || r.RepoID == name {
			return r, nil
		}
	}
	return socket.RepoRulesInfo{}, fmt.Errorf("no repository named %s is registered on this host", name)
}

// conversationOf names a session the way the inbox does: by its title, or
// its first prompt until it has one.
func conversationOf(s socket.SessionInfo) string {
	if s.Title != "" {
		return s.Title
	}
	return s.Prompt
}

// whereOf says where a session works: the checkout, or its own branch.
func whereOf(s socket.SessionInfo) string {
	switch {
	case s.WorktreeRemoved:
		return "removed"
	case s.InCheckout:
		return "checkout (" + s.Branch + ")"
	default:
		return s.Branch
	}
}

// changesOf says what became of a session's changes through Skylands.
func changesOf(s socket.SessionInfo) string {
	var parts []string
	if s.MergedInto != "" {
		parts = append(parts, "merged into "+s.MergedInto)
	}
	if s.PullRequest != "" {
		parts = append(parts, "pull request "+s.PullRequest)
	}
	if len(parts) == 0 && s.Commit != "" {
		short := s.Commit
		if len(short) > 7 {
			short = short[:7]
		}
		parts = append(parts, "committed "+short)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func newRequestAnswerCmd() *cobra.Command {
	var socketPath string
	var decline bool
	cmd := &cobra.Command{
		Use:   "answer <question> [field=value ...]",
		Short: "Answer a question an agent is waiting on, or decline it",
		Long: `Request answer gives a waiting question its answers, one field=value per
field as request list shows them. A field that offers choices takes one of
them, or several separated by commas when it allows that. With --decline
the agent is told you will not answer.`,
		Example: `  skylands request answer 7Fq2 question_0=a.go
  skylands request answer 7Fq2 question_0=a.go "question_0_custom=only the header"
  skylands request answer 7Fq2 --decline`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli := socket.Dial(socketPath)
			var answers json.RawMessage
			if !decline {
				questions, err := cli.Questions(cmd.Context())
				if err != nil {
					return err
				}
				var fields []v1.QuestionField
				for _, q := range questions {
					if q.ID == args[0] {
						fields = q.Fields
					}
				}
				values := map[string]any{}
				for _, kv := range args[1:] {
					k, v, ok := strings.Cut(kv, "=")
					if !ok {
						return fmt.Errorf("%q is not field=value", kv)
					}
					values[k] = v
					for _, f := range fields {
						if f.Key == k && f.Type == "array" {
							var items []string
							for _, it := range strings.Split(v, ",") {
								if it = strings.TrimSpace(it); it != "" {
									items = append(items, it)
								}
							}
							values[k] = items
						}
					}
				}
				if len(values) == 0 {
					return fmt.Errorf("give at least one field=value, or --decline")
				}
				answers, _ = json.Marshal(values)
			}
			q, err := cli.AnswerQuestion(cmd.Context(), args[0], answers)
			if err != nil {
				return err
			}
			if decline {
				fmt.Fprintf(cmd.OutOrStdout(), "Declined %q for %s in %s\n", cut(q.Message, 60), q.Agent, q.Repo)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Answered %q for %s in %s\n", cut(q.Message, 60), q.Agent, q.Repo)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&decline, "decline", false, "tell the agent you will not answer")
	socketFlag(cmd, &socketPath)
	return cmd
}

// fieldText describes one question field: its key, what it is called, and
// the choices it offers.
func fieldText(f v1.QuestionField) string {
	s := f.Key
	if f.Title != "" {
		s += " (" + f.Title + ")"
	}
	if len(f.Options) > 0 {
		var opts []string
		for _, o := range f.Options {
			opts = append(opts, o.Value)
		}
		s += ": " + strings.Join(opts, " | ")
		if f.Type == "array" {
			s += " (several, comma-separated)"
		}
	} else {
		s += ": free text"
	}
	return s
}

// cut shortens s to n characters with an ellipsis, on one line.
func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "\u2026"
	}
	return s
}
