package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/simon/crabctl/internal/tmux"
	"github.com/simon/crabctl/internal/tui"
)

func SetVersionInfo(version, commit string) {
	rootCmd.Version = fmt.Sprintf("%s (%s)", version, commit)
}

var rootCmd = &cobra.Command{
	Use:   "crabctl",
	Short: "Manage Claude Code sessions in tmux",
	RunE: func(cmd *cobra.Command, args []string) error {
		for {
			m := tui.NewModel()
			p := tea.NewProgram(m, tea.WithAltScreen())

			finalModel, err := p.Run()
			if err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}

			final := finalModel.(tui.Model)
			if final.AttachTarget == "" {
				break
			}

			// Attach as child process; returns when user detaches
			_ = tmux.RunAttachSession(final.AttachTarget)
			// Loop restarts TUI
		}

		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
