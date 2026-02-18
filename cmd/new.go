package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/simon/crabctl/internal/session"
	"github.com/spf13/cobra"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var newCmd = &cobra.Command{
	Use:   "new <[host:]name> [message...]",
	Short: "Create a new Claude session",
	Args:  cobra.MinimumNArgs(1),
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

		// Collect message from remaining args or -m flag
		msgFlag, _ := cmd.Flags().GetString("message")
		message := msgFlag
		if message == "" && len(args) > 1 {
			message = strings.Join(args[1:], " ")
		}

		var claudeArgs []string
		claudeArgs = append(claudeArgs, "--dangerously-skip-permissions")

		if err := exec.NewSession(name, dir, claudeArgs); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}

		fmt.Printf("Created session %q\n", args[0])

		if message != "" {
			if err := waitForPrompt(exec, fullName); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v (session created but message not sent)\n", err)
				return nil
			}
			if err := exec.SendKeys(fullName, message); err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}
			fmt.Printf("Sent: %s\n", message)
		}

		if attach {
			return exec.AttachSession(fullName)
		}

		return nil
	},
}

// waitForPrompt polls the pane until Claude shows the ❯ prompt.
func waitForPrompt(exec interface {
	CapturePaneOutput(string, int) (string, error)
}, fullName string) error {
	timeout := 30 * time.Second
	poll := 500 * time.Millisecond
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		time.Sleep(poll)
		output, err := exec.CapturePaneOutput(fullName, 10)
		if err != nil {
			continue
		}
		status := session.DetectStatus(output)
		if status == session.Waiting {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for Claude prompt (%v)", timeout)
}

func init() {
	newCmd.Flags().StringP("dir", "c", "", "Working directory for the session")
	newCmd.Flags().StringP("message", "m", "", "Message to send once Claude is ready")
	newCmd.Flags().BoolP("attach", "a", false, "Attach to the session immediately")
	rootCmd.AddCommand(newCmd)
}
