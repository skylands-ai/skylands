package cli

// The local CLI talks to the daemon on this host over its Unix socket. It
// registers repos, lists what is waiting on a person, approves or denies
// those requests, lists, stops, and cleans up sessions, and reports status.
// The daemon accepts these from the OS user that owns it, which unblocks
// agents even while the host is offline.

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/daemon/socket"
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
	cmd.AddCommand(newRequestListCmd(), newRequestApproveCmd(), newRequestDenyCmd())
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
			reqs, err := socket.Dial(socketPath).Requests(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(reqs) == 0 {
				fmt.Fprintln(out, "Nothing is waiting on you.")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "REQUEST\tCONVERSATION\tAGENT\tREPO\tTOOL\tWAITING")
			for _, r := range reqs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, cut(r.Conversation, 40), r.Agent, r.Repo, cut(r.Tool, 50), time.Since(r.Since).Truncate(time.Second))
			}
			return tw.Flush()
		},
	}
	socketFlag(cmd, &socketPath)
	return cmd
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
	cmd.AddCommand(newSessionListCmd(), newSessionStopCmd(), newSessionRemoveCmd(), newSessionPruneCmd())
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

// cut shortens s to n characters with an ellipsis, on one line.
func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "\u2026"
	}
	return s
}
