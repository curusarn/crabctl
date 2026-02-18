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
	Use:   "kill <[host:]name>",
	Short: "Kill a Claude session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, name := parseHostName(args[0])
		exec := resolveExecutor(host)
		fullName := tmux.SessionPrefix + name

		if !exec.HasSession(fullName) {
			return fmt.Errorf("session %q not found", args[0])
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("Kill session %q? [y/N] ", args[0])
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if err := exec.KillSession(fullName); err != nil {
			return fmt.Errorf("failed to kill session: %w", err)
		}

		fmt.Printf("Killed session %q\n", args[0])
		return nil
	},
}

func init() {
	killCmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	rootCmd.AddCommand(killCmd)
}
