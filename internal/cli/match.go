package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/daemon/socket"
	"github.com/skylands-ai/skylands/internal/dispatch"
)

func newRepoMatchCmd() *cobra.Command {
	var socketPath string
	var replay bool
	cmd := &cobra.Command{
		Use:   "match [prompt]",
		Short: "Show how a prompt scores against this island's repositories",
		Long: `Repo match scores a prompt against this island's repositories the way the
command bar does when work is asked for at the world or this island, and
lists each repository it matches, best first, with its confidence and what
matched where. Other islands aren't asked.

With --replay, it places each past session's first prompt on this island
the same way, and reports how often the work would have gone to the
repository the session ran in.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if replay && len(args) > 0 {
				return fmt.Errorf("--replay takes no prompt")
			}
			if !replay && len(args) == 0 {
				return fmt.Errorf("give a prompt, or --replay")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cli := socket.Dial(socketPath)
			out := cmd.OutOrStdout()
			if replay {
				sessions, err := cli.Replay(cmd.Context())
				if err != nil {
					return err
				}
				printReplay(out, sessions)
				return nil
			}
			info, err := cli.Match(cmd.Context(), strings.Join(args, " "))
			if err != nil {
				return err
			}
			printMatch(out, info)
			return nil
		},
	}
	cmd.Flags().BoolVar(&replay, "replay", false, "place every past session's first prompt and report how often it would go to its own repository")
	socketFlag(cmd, &socketPath)
	return cmd
}

func printMatch(out io.Writer, info socket.MatchInfo) {
	switch {
	case info.Repos == 0:
		fmt.Fprintln(out, "No repository is registered on this island.")
		return
	case len(info.Verdict.Ranked) == 0:
		fmt.Fprintln(out, "No repository on this island matches.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tCONFIDENCE\tMATCHED")
	for _, c := range info.Verdict.Ranked {
		fmt.Fprintf(tw, "%s\t%.2f\t%s\n", c.Name, c.Confidence, strings.Join(c.Reasons, "; "))
	}
	_ = tw.Flush()
}

// replayTally is how a replay of past sessions came out.
type replayTally struct {
	sessions int
	// right started in their own repository, wrong in another.
	right, wrong int
	// agent went to the agent, and agentHad of them offered it their own
	// repository.
	agent, agentHad int
	person          int
	// misses are the sessions that would have started elsewhere.
	misses []socket.ReplayedSession
}

func tallyReplay(sessions []socket.ReplayedSession) replayTally {
	t := replayTally{sessions: len(sessions)}
	for _, s := range sessions {
		switch s.Verdict.Do {
		case dispatch.Start:
			if s.Verdict.Target.RepoID == s.RepoID {
				t.right++
			} else {
				t.wrong++
				t.misses = append(t.misses, s)
			}
		case dispatch.AskAgent:
			t.agent++
			for _, c := range s.Verdict.Among {
				if c.RepoID == s.RepoID {
					t.agentHad++
				}
			}
		default:
			t.person++
		}
	}
	return t
}

func printReplay(out io.Writer, sessions []socket.ReplayedSession) {
	if len(sessions) == 0 {
		fmt.Fprintln(out, "No past session on this island has a prompt to replay.")
		return
	}
	t := tallyReplay(sessions)
	pct := func(n int) string { return fmt.Sprintf("%d%%", n*100/t.sessions) }
	fmt.Fprintf(out, "Placed the first prompts of %d past sessions on this island:\n", t.sessions)
	fmt.Fprintf(out, "  %d would start in their own repository (%s)\n", t.right, pct(t.right))
	fmt.Fprintf(out, "  %d would start in another (%s)\n", t.wrong, pct(t.wrong))
	fmt.Fprintf(out, "  %d would go to the island's agent, %d of them with their own repository among its choices\n", t.agent, t.agentHad)
	fmt.Fprintf(out, "  %d would go to a person\n", t.person)
	if len(t.misses) == 0 {
		return
	}
	fmt.Fprintln(out, "\nWould start in another repository:")
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tRAN IN\tWOULD START IN\tPROMPT")
	for _, s := range t.misses {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Session, s.Repo, s.Verdict.Target.Name, cut(s.Prompt, 60))
	}
	_ = tw.Flush()
}
