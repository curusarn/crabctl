package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/simon/crabctl/internal/tmux"
)

var sendCmd = &cobra.Command{
	Use:   "send <[host:]name> <text...>",
	Short: "Send text to a Claude session",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, name := parseHostName(args[0])
		text := strings.Join(args[1:], " ")
		exec := resolveExecutor(host)
		fullName := tmux.SessionPrefix + name

		if !exec.HasSession(fullName) {
			return fmt.Errorf("session %q not found", args[0])
		}

		if err := exec.SendKeys(fullName, text); err != nil {
			return fmt.Errorf("failed to send: %w", err)
		}

		fmt.Printf("Sent to %q: %s\n", args[0], text)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(sendCmd)
}
