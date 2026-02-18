package cmd

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/simon/crabctl/internal/config"
	"github.com/simon/crabctl/internal/tmux"
	"github.com/simon/crabctl/internal/tui"
)

func SetVersionInfo(version, commit string) {
	rootCmd.Version = fmt.Sprintf("%s (%s)", version, commit)
}

func buildExecutors() []tmux.Executor {
	executors := []tmux.Executor{&tmux.LocalExecutor{}}

	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return executors
	}

	for nickname, h := range cfg.Hosts {
		executors = append(executors, &tmux.SSHExecutor{
			Nickname: nickname,
			Host:     h.Host,
			User:     h.User,
			SSHKey:   h.SSHKey,
			Prefix:   h.Prefix,
		})
	}

	return executors
}

var rootCmd = &cobra.Command{
	Use:   "crabctl",
	Short: "Manage Claude Code sessions in tmux",
	RunE: func(cmd *cobra.Command, args []string) error {
		executors := buildExecutors()

		for {
			m := tui.NewModel(executors)
			p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

			finalModel, err := p.Run()
			if err != nil {
				return fmt.Errorf("TUI error: %w", err)
			}

			final := finalModel.(tui.Model)
			if final.AttachTarget == "" {
				break
			}

			// Attach via the correct executor
			exec := findExecutorByHost(executors, final.AttachHost)
			_ = exec.AttachSession(final.AttachTarget)
			// Loop restarts TUI
		}

		return nil
	},
}

func findExecutorByHost(executors []tmux.Executor, host string) tmux.Executor {
	for _, e := range executors {
		if e.HostName() == host {
			return e
		}
	}
	return &tmux.LocalExecutor{}
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
