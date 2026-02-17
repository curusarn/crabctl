package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/simon/crabctl/internal/tmux"
)

var sendCmd = &cobra.Command{
	Use:   "send <name> <text...>",
	Short: "Send text to a Claude session",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		text := strings.Join(args[1:], " ")
		fullName := tmux.SessionPrefix + name

		if !tmux.HasSession(fullName) {
			return fmt.Errorf("session %q not found", name)
		}

		if err := tmux.SendKeys(fullName, text); err != nil {
			return fmt.Errorf("failed to send: %w", err)
		}

		fmt.Printf("Sent to %q: %s\n", name, text)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(sendCmd)
}
