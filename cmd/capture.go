package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var captureLines int

var captureCmd = &cobra.Command{
	Use:   "capture <[host:]name>",
	Short: "Capture pane output with ghost text stripped",
	Long: `Capture tmux pane output from a Claude session, stripping autocomplete
ghost text and ANSI codes. Drop-in replacement for tmux capture-pane
that produces clean output safe for LLM consumption.

Equivalent to: tmux capture-pane -t SESSION -p -S -LINES
but also strips dim/autocomplete suggestions that Claude Code renders.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, name := parseHostName(args[0])
		exec := resolveExecutor(host)
		fullName := resolveFullName(exec, name)

		if !exec.HasSession(fullName) {
			return fmt.Errorf("session %q not found", args[0])
		}

		output, err := exec.CapturePaneOutput(fullName, captureLines)
		if err != nil {
			return fmt.Errorf("capture failed: %w", err)
		}

		fmt.Fprint(os.Stdout, output)
		return nil
	},
}

func init() {
	captureCmd.Flags().IntVarP(&captureLines, "lines", "S", 30, "Number of lines to capture from the bottom")
	rootCmd.AddCommand(captureCmd)
}
