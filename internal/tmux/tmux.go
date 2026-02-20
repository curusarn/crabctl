package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
	return listSessionsWithPrefix(SessionPrefix)
}

// runCommand executes a command and returns its output as a string.
func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// CapturePaneOutput captures the last N lines from a tmux pane.
// Captures with ANSI escape codes (-e) to detect and strip dim/gray
// suggestion text (autocomplete ghosts) that Claude Code renders,
// then strips all remaining ANSI codes.
func CapturePaneOutput(fullName string, lines int) (string, error) {
	tmux, err := FindTmux()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(tmux, "capture-pane", "-t", fullName, "-p", "-e", "-S", fmt.Sprintf("-%d", lines))
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	cleaned := stripDimText(string(out))
	cleaned = ansiRe.ReplaceAllString(cleaned, "")
	return cleaned, nil
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripDimText removes text rendered with dim (SGR 2) or bright-black
// (SGR 90) ANSI styling. Claude Code uses these for autocomplete
// suggestions that appear as gray ghost text at the prompt.
func stripDimText(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	dim := false
	i := 0
	for i < len(s) {
		// Detect ESC[ CSI sequence
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				params := s[i+2 : j]
				j++
				if hasSGR(params, "2") || hasSGR(params, "90") || hasSGR(params, "7") {
					dim = true
					i = j
					continue
				}
				if params == "" || params == "0" || hasSGR(params, "0") ||
					hasSGR(params, "22") || hasSGR(params, "27") || hasSGR(params, "39") {
					dim = false
				}
				if !dim {
					buf.WriteString(s[i:j])
				}
				i = j
				continue
			}
		}
		if !dim {
			buf.WriteByte(s[i])
		}
		i++
	}
	return buf.String()
}

func hasSGR(params, code string) bool {
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		// Skip extended color sequences where "2" or "5" are color mode
		// indicators, not standalone SGR params:
		//   38;2;R;G;B (24-bit fg)  38;5;N (256-color fg)
		//   48;2;R;G;B (24-bit bg)  48;5;N (256-color bg)
		if (p == "38" || p == "48") && i+1 < len(parts) {
			switch parts[i+1] {
			case "2":
				i += 4 // skip ;2;R;G;B
				continue
			case "5":
				i += 2 // skip ;5;N
				continue
			}
		}
		if p == code {
			return true
		}
	}
	return false
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

// GetSessionCreated returns the creation time of a tmux session.
func GetSessionCreated(fullName string) time.Time {
	tmuxBin, err := FindTmux()
	if err != nil {
		return time.Time{}
	}

	cmd := exec.Command(tmuxBin, "display-message", "-t", fullName, "-p", "#{session_created}")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}
	}
	epoch, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(epoch, 0)
}
