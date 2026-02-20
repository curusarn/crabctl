package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/simon/crabctl/internal/state"
)

var setCmd = &cobra.Command{
	Use:   "set <[host:]name>",
	Short: "Set session options (e.g. autoforward)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, name := parseHostName(args[0])
		exec := resolveExecutor(host)
		fullName := exec.SessionPrefix() + name

		if !exec.HasSession(fullName) {
			return fmt.Errorf("session %q not found", args[0])
		}

		af, _ := cmd.Flags().GetBool("autoforward")
		stopAf, _ := cmd.Flags().GetBool("stop-autoforward")

		if !af && !stopAf {
			return fmt.Errorf("specify -a/--autoforward or -A/--stop-autoforward")
		}

		store, err := state.Open()
		if err != nil {
			return fmt.Errorf("failed to open state db: %w", err)
		}
		defer store.Close()

		if af {
			if err := store.SetAutoForward(fullName, true); err != nil {
				return fmt.Errorf("failed to set autoforward: %w", err)
			}
			fmt.Printf("Enabled autoforward for %q\n", args[0])
		}

		if stopAf {
			if err := store.SetAutoForward(fullName, false); err != nil {
				return fmt.Errorf("failed to disable autoforward: %w", err)
			}
			fmt.Printf("Disabled autoforward for %q\n", args[0])
		}

		return nil
	},
}

func init() {
	setCmd.Flags().BoolP("autoforward", "a", false, "Enable autoforward")
	setCmd.Flags().BoolP("stop-autoforward", "A", false, "Disable autoforward")
	rootCmd.AddCommand(setCmd)
}
