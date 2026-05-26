package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/simon/crabctl/internal/session"
	"github.com/simon/crabctl/internal/state"
	"github.com/simon/crabctl/internal/tmux"
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

		// Detect parent session
		parentFlag, _ := cmd.Flags().GetString("parent")
		parent := tmux.DetectParent(parentFlag)

		var claudeArgs []string
		claudeArgs = append(claudeArgs, "--dangerously-skip-permissions")

		if err := exec.NewSession(name, dir, claudeArgs, parent); err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}

		// Save parent relationship to DB
		if parent != "" {
			sessionKey := session.SessionKey(host, fullName)
			if store, err := state.Open(); err == nil {
				defer store.Close()
				store.SaveParent(sessionKey, parent)
			}
		}

		fmt.Printf("Created session %q\n", args[0])
		if parent != "" {
			fmt.Printf("Parent: %s\n", parent)
		}

		if message != "" {
			if err := waitForPrompt(exec, fullName); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: %v (session created but message not sent)\n", err)
				return nil
			}
			if err := sendMessage(exec, fullName, message); err != nil {
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

type promptDetector interface {
	CapturePaneOutput(string, int) (string, error)
	SendKeys(string, string) error
}

// sendEnter is the post-paste Enter resend used by sendMessage. It's a
// package var so tests can override it without spinning up real tmux.
var sendEnter = tmux.SendEnter

// waitForPrompt polls the pane until Claude shows the ❯ prompt.
// If Claude shows the "Do you trust the files in this folder?" prompt
// first (always shown for never-seen-before directories), auto-accept it
// by sending "1" so the new-session message flow doesn't time out.
func waitForPrompt(exec promptDetector, fullName string) error {
	timeout := 30 * time.Second
	poll := 500 * time.Millisecond
	deadline := time.Now().Add(timeout)
	trustHandled := false

	for time.Now().Before(deadline) {
		time.Sleep(poll)
		// Capture enough lines to cover the multi-line trust prompt body
		// (header + paragraph + numbered options).
		output, err := exec.CapturePaneOutput(fullName, 30)
		if err != nil {
			continue
		}
		if !trustHandled && isTrustPrompt(output) {
			// SendKeys sends the text then Enter as separate send-keys calls.
			if err := exec.SendKeys(fullName, "1"); err == nil {
				trustHandled = true
				// Give Claude a moment to dismiss the prompt and render
				// the main UI before re-checking status.
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		status := session.DetectStatus(output)
		if status == session.Waiting {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for Claude prompt (%v)", timeout)
}

// isTrustPrompt detects Claude Code's initial trust-this-folder prompt,
// which is shown the first time Claude is launched in a directory and
// blocks the main UI until answered. Wording has varied across Claude
// versions, so match on multiple distinctive substrings.
func isTrustPrompt(output string) bool {
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	return strings.Contains(lower, "trust this folder") ||
		strings.Contains(lower, "trust the files in this folder") ||
		strings.Contains(lower, "do you trust the files")
}

// sendMessage sends a message and verifies Claude started processing it.
// Retries the Enter key (up to 2 retries) if Claude is still waiting,
// to cover the case where the initial Enter was absorbed into a
// multi-chunk paste despite the post-paste settle in tmux.SendKeys.
func sendMessage(exec promptDetector, fullName, message string) error {
	if err := exec.SendKeys(fullName, message); err != nil {
		return err
	}

	// Verify Claude started processing (transitioned away from Waiting)
	for i := 0; i < 2; i++ {
		time.Sleep(500 * time.Millisecond)
		output, err := exec.CapturePaneOutput(fullName, 10)
		if err != nil {
			continue
		}
		status := session.DetectStatus(output)
		if status != session.Waiting {
			return nil // Claude is processing
		}
		// Still waiting — the Enter key might have been lost, resend just Enter
		sendEnter(fullName)
	}
	return nil // sent text, best effort
}

func init() {
	newCmd.Flags().StringP("dir", "c", "", "Working directory for the session")
	newCmd.Flags().StringP("message", "m", "", "Message to send once Claude is ready")
	newCmd.Flags().BoolP("attach", "a", false, "Attach to the session immediately")
	newCmd.Flags().StringP("parent", "p", "", "Parent session name (auto-detected if not set)")
	rootCmd.AddCommand(newCmd)
}
