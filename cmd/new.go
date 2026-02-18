package cmd

import (
	"fmt"
	"os"
	"regexp"

	"github.com/spf13/cobra"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var newCmd = &cobra.Command{
	Use:   "new <[host:]name>",
	Short: "Create a new Claude session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, name := parseHostName(args[0])
		if !validName.MatchString(name) {
			return fmt.Errorf("invalid name %q: use only alphanumeric, hyphens, underscores", name)
		}

		exec := resolveExecutor(host)
		fullName := exec.SessionPrefix() + name
		if exec.HasSession(fullName) {
			return fmt.Errorf("session %q already exists", args[0])
		}

		dir, _ := cmd.Flags().GetString("dir")
		if dir == "" && host == "" {
			dir, _ = os.Getwd()
		}
		attach, _ := cmd.Flags().GetBool("attach")

		var claudeArgs []string
		claudeArgs = append(claudeArgs, "--dangerously-skip-permissions")

		if err := exec.NewSession(name, dir, claudeArgs); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}

		fmt.Printf("Created session %q\n", args[0])

		if attach {
			return exec.AttachSession(fullName)
		}

		return nil
	},
}

func init() {
	newCmd.Flags().StringP("dir", "c", "", "Working directory for the session")
	newCmd.Flags().BoolP("attach", "a", false, "Attach to the session immediately")
	rootCmd.AddCommand(newCmd)
}
