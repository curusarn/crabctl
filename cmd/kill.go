package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/simon/crabctl/internal/tmux"
)

var killCmd = &cobra.Command{
	Use:   "kill <name>",
	Short: "Kill a Claude session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		fullName := tmux.SessionPrefix + name

		if !tmux.HasSession(fullName) {
			return fmt.Errorf("session %q not found", name)
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("Kill session %q? [y/N] ", name)
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if err := tmux.KillSession(fullName); err != nil {
			return fmt.Errorf("failed to kill session: %w", err)
		}

		fmt.Printf("Killed session %q\n", name)
		return nil
	},
}

func init() {
	killCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	rootCmd.AddCommand(killCmd)
}
