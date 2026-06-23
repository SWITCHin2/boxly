// Package cli implements the boxly command-line client.
package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/SWITCHin2/boxly/internal/tui"
	"github.com/SWITCHin2/boxly/pkg/client"
)

var (
	flagServer string
	flagToken  string
)

// NewRootCmd builds the boxly root command and its subcommands.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "boxly",
		Short:         "Boxly — instant dev containers, ready in a second",
		SilenceUsage:  true,
		SilenceErrors: false,
		// Bare `boxly` opens the live full-screen dashboard — the client surface
		// where users create, connect to, and manage their boxes.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tui.Run(newClient(), flagServer, flagToken)
		},
	}
	root.PersistentFlags().StringVar(&flagServer, "server", env("BOXLY_SERVER", "http://localhost:8080"), "boxlyd base URL")
	root.PersistentFlags().StringVar(&flagToken, "token", env("BOXLY_TOKEN", "dev-secret"), "API bearer token")

	root.AddCommand(newVMCmd(), newSSHCmd(), newExecCmd(), newLaunchCmd(), newAdminCmd(), newDashCmd())
	return root
}

func newDashCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "dash",
		Aliases: []string{"ui"},
		Short:   "Open the live full-screen dashboard",
		RunE:    func(_ *cobra.Command, _ []string) error { return tui.Run(newClient(), flagServer, flagToken) },
	}
}

func newClient() *client.Client { return client.New(flagServer, flagToken) }

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
