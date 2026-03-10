package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/simon/crabctl/internal/session"
	"github.com/simon/crabctl/internal/state"
	"github.com/spf13/cobra"
)

var sendCmd = &cobra.Command{
	Use:   "send <[host:]name> <text...>",
	Short: "Send text to a Claude session",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, name := parseHostName(args[0])
		text := strings.Join(args[1:], " ")
		exec := resolveExecutor(host)
		fullName := resolveFullName(exec, name)

		if !exec.HasSession(fullName) {
			return fmt.Errorf("session %q not found", args[0])
		}

		if err := exec.SendKeys(fullName, text); err != nil {
			return fmt.Errorf("failed to send: %w", err)
		}

		fmt.Printf("Sent to %q: %s\n", args[0], text)

		// Record interaction time and resolve session ID from history
		if store, err := state.Open(); err == nil {
			defer store.Close()
			key := session.SessionKey(host, fullName)
			_ = store.SaveInteraction(key)

			// Capture workDir before sleeping — pane path is stable now
			workDir := exec.GetPanePath(fullName)

			// Wait for Claude to process the message and write to history
			time.Sleep(3 * time.Second)
			historyContent, err := exec.ReadHistoryTail(100)
			if err == nil && historyContent != "" {
				sessionID := session.FindSessionIDByMessage(historyContent, text, 10*time.Second, workDir)
				if sessionID != "" {
					_ = store.SaveSessionUUID(key, sessionID, workDir, "")
				}
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(sendCmd)
}
