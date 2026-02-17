package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const SessionPrefix = "crab-"

type SessionInfo struct {
	Name          string
	FullName      string // with crab- prefix
	AttachedCount int
	Created       time.Time
}

// FindTmux locates the tmux binary.
func FindTmux() (string, error) {
	return exec.LookPath("tmux")
}

// ListSessions returns all crab-* tmux sessions.
func ListSessions() ([]SessionInfo, error) {
	tmux, err := FindTmux()
	if err != nil {
		return nil, fmt.Errorf("tmux not found: %w", err)
	}

	cmd := exec.Command(tmux, "list-sessions", "-F", "#{session_name}|#{session_attached}|#{session_created}")
	out, err := cmd.Output()
	if err != nil {
		// "no server running" or "no sessions" — not an error for us
		return nil, nil
	}

	var sessions []SessionInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		fullName := parts[0]
		if !strings.HasPrefix(fullName, SessionPrefix) {
			continue
		}

		attached, _ := strconv.Atoi(parts[1])
		createdUnix, _ := strconv.ParseInt(parts[2], 10, 64)

		sessions = append(sessions, SessionInfo{
			Name:          strings.TrimPrefix(fullName, SessionPrefix),
			FullName:      fullName,
			AttachedCount: attached,
			Created:       time.Unix(createdUnix, 0),
		})
	}
	return sessions, nil
}

// CapturePaneOutput captures the last N lines from a tmux pane.
func CapturePaneOutput(fullName string, lines int) (string, error) {
	tmux, err := FindTmux()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(tmux, "capture-pane", "-t", fullName, "-p", "-S", fmt.Sprintf("-%d", lines))
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// NewSession creates a new detached tmux session running claude.
func NewSession(name, workDir string, claudeArgs []string) error {
	tmux, err := FindTmux()
	if err != nil {
		return err
	}

	fullName := SessionPrefix + name
	args := []string{"new-session", "-d", "-s", fullName}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}

	// Build the claude command, unsetting CLAUDECODE to allow nesting
	claudeCmd := "unset CLAUDECODE; claude"
	for _, a := range claudeArgs {
		claudeCmd += " " + a
	}
	args = append(args, claudeCmd)

	cmd := exec.Command(tmux, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}

	// Store claude flags as tmux session environment variable
	if len(claudeArgs) > 0 {
		setEnv := exec.Command(tmux, "set-environment", "-t", fullName,
			"CRABCTL_FLAGS", strings.Join(claudeArgs, " "))
		_ = setEnv.Run()
	}

	return nil
}

// GetSessionEnv reads a tmux environment variable from a session.
func GetSessionEnv(fullName, key string) string {
	tmux, err := FindTmux()
	if err != nil {
		return ""
	}

	cmd := exec.Command(tmux, "show-environment", "-t", fullName, key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// Output is "KEY=value\n"
	s := strings.TrimSpace(string(out))
	if idx := strings.Index(s, "="); idx >= 0 {
		return s[idx+1:]
	}
	return ""
}

// HasSession checks if a tmux session exists.
func HasSession(fullName string) bool {
	tmux, err := FindTmux()
	if err != nil {
		return false
	}

	cmd := exec.Command(tmux, "has-session", "-t", fullName)
	return cmd.Run() == nil
}

// KillSession sends Ctrl-C, waits briefly, then kills the session.
func KillSession(fullName string) error {
	tmux, err := FindTmux()
	if err != nil {
		return err
	}

	// Send Ctrl-C first
	cmd := exec.Command(tmux, "send-keys", "-t", fullName, "C-c", "")
	_ = cmd.Run()

	time.Sleep(500 * time.Millisecond)

	cmd = exec.Command(tmux, "kill-session", "-t", fullName)
	return cmd.Run()
}

// SendKeys sends text followed by Enter to a tmux session.
// Uses -l flag for literal text (no key name interpretation), then
// sends Enter separately to submit.
func SendKeys(fullName, text string) error {
	tmux, err := FindTmux()
	if err != nil {
		return err
	}

	// Send the text literally (without interpreting key names)
	cmd := exec.Command(tmux, "send-keys", "-t", fullName, "-l", text)
	if err := cmd.Run(); err != nil {
		return err
	}

	// Send Enter to submit
	cmd = exec.Command(tmux, "send-keys", "-t", fullName, "Enter")
	return cmd.Run()
}

// filterTMUX removes the TMUX env var so we can attach from within tmux.
func filterTMUX(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "TMUX=") {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// AttachSession replaces the current process with tmux attach.
func AttachSession(fullName string) error {
	tmux, err := FindTmux()
	if err != nil {
		return err
	}

	return syscall.Exec(tmux, []string{"tmux", "attach-session", "-t", fullName}, filterTMUX(os.Environ()))
}

// RunAttachSession runs tmux attach as a child process (returns on detach).
func RunAttachSession(fullName string) error {
	tmuxBin, err := FindTmux()
	if err != nil {
		return err
	}

	cmd := exec.Command(tmuxBin, "attach-session", "-t", fullName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = filterTMUX(os.Environ())
	return cmd.Run()
}

// GetPanePath returns the current working directory of a session's active pane.
func GetPanePath(fullName string) string {
	tmuxBin, err := FindTmux()
	if err != nil {
		return ""
	}

	cmd := exec.Command(tmuxBin, "display-message", "-t", fullName, "-p", "#{pane_current_path}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
