package cli

import (
	"github.com/spf13/cobra"

	"github.com/skylands-ai/skylands/internal/fakeagent"
)

// newFakeAgentCmd runs the built-in scripted agent over stdio. The daemon's
// "fake" adapter launches the skylands binary this way, so demos and tests
// need no real agent.
func newFakeAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "fakeagent",
		Short:  "Run the built-in scripted ACP agent over stdio",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fakeagent.Run(cmd.Context())
		},
	}
}
