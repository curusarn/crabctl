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
	Parent        string // from CRABCTL_PARENT env var
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

// sgrRe matches CSI SGR sequences: ESC[ <params> m
var sgrRe = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// stripDimText removes text rendered with dim (SGR 2), bright-black
// (SGR 90), or reverse-video (SGR 7) ANSI styling. Claude Code uses
// these for autocomplete ghost text at the prompt.
//
// Flow: find all SGR sequences via regex, track dim state, copy only
// non-dim text segments. Non-SGR ANSI codes pass through (stripped
// later by ansiRe).
func stripDimText(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	dim := false
	last := 0

	for _, loc := range sgrRe.FindAllStringSubmatchIndex(s, -1) {
		// loc[0:2] = full match, loc[2:4] = captured params group
		if !dim {
			buf.WriteString(s[last:loc[0]])
		}

		params := s[loc[2]:loc[3]]
		codes := parseSGRCodes(params)

		if containsAny(codes, "2", "90", "7") {
			dim = true
		} else if params == "" || containsAny(codes, "0", "22", "27", "39") {
			dim = false
		}

		if !dim {
			buf.WriteString(s[loc[0]:loc[1]])
		}
		last = loc[1]
	}

	if !dim {
		buf.WriteString(s[last:])
	}
	return buf.String()
}

// parseSGRCodes splits semicolon-separated SGR params, skipping
// extended color sequences so their sub-params aren't misinterpreted:
//
//	38;2;R;G;B  (24-bit fg color — "2" here is a color mode, not SGR dim)
//	38;5;N      (256-color fg)
//	48;2;R;G;B  (24-bit bg)
//	48;5;N      (256-color bg)
func parseSGRCodes(params string) []string {
	parts := strings.Split(params, ";")
	codes := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if (p == "38" || p == "48") && i+1 < len(parts) {
			switch parts[i+1] {
			case "2":
				i += 4 // skip 38;2;R;G;B
				continue
			case "5":
				i += 2 // skip 38;5;N
				continue
			}
		}
		codes = append(codes, p)
	}
	return codes
}

func containsAny(codes []string, targets ...string) bool {
	for _, c := range codes {
		for _, t := range targets {
			if c == t {
				return true
			}
		}
	}
	return false
}

// NewSession creates a new detached tmux session running claude.
func NewSession(name, workDir string, claudeArgs []string, parent string) error {
	tmux, err := FindTmux()
	if err != nil {
		return err
	}

	fullName := SessionPrefix + name
	// Respawning a session from inside itself (e.g. `crabctl new orchestrator`
	// run in crab-orchestrator) would otherwise record it as its own parent.
	if parent == fullName {
		parent = ""
	}
	args := []string{"new-session", "-d", "-s", fullName}
	// Set CRABCTL_NAME so the child session knows its own identity
	args = append(args, "-e", "CRABCTL_NAME="+fullName)
	if workDir != "" {
		args = append(args, "-c", workDir)
	}

	// Build the claude command, unsetting CLAUDECODE to allow nesting.
	// Each crab gets its own chrome-devtools profile so parallel crabs don't
	// collide on one browser profile ("browser already running"). ~/.claude.json
	// resolves ${CDP_PROFILE:-$HOME/.cache/cdp-profile} in the MCP args; agents
	// can re-export the shared warm profile as a captcha fallback.
	claudeCmd := cdpProfileExport(fullName) + "unset CLAUDECODE; claude"
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

	// Store parent reference as tmux session environment variable
	if parent != "" {
		setEnv := exec.Command(tmux, "set-environment", "-t", fullName,
			"CRABCTL_PARENT", parent)
		_ = setEnv.Run()
	}

	return nil
}

// cdpProfileExport returns the shell fragment that points CDP_PROFILE at a
// per-session chrome-devtools profile dir. $HOME is left for the target shell
// to expand (matters for SSH hosts with a different home).
func cdpProfileExport(fullName string) string {
	return `export CDP_PROFILE="$HOME/.cache/cdp-profile-` + fullName + `"; `
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

	// Pause before sending Enter. Large messages stream into Claude
	// Code fast enough that its TUI groups them into one or more
	// "[Pasted text #N +K lines]" markers. If Enter arrives while the
	// paste detector is still buffering, it gets absorbed into the
	// last paste segment as a literal newline rather than submitting
	// the input. Letting the paste settle first makes Enter behave as
	// a submit even for multi-chunk pastes.
	time.Sleep(PostPasteSettleDelay)

	// Send Enter to submit
	cmd = exec.Command(tmux, "send-keys", "-t", fullName, "Enter")
	return cmd.Run()
}

// PostPasteSettleDelay is the pause between the literal paste and the
// Enter keystroke. ~200ms is enough for Claude Code to finalize all
// "[Pasted text #N]" markers for messages we've observed (up to ~5KB)
// without noticeably slowing down the new-session flow.
const PostPasteSettleDelay = 200 * time.Millisecond

// SendEnter sends just the Enter key to a session.
func SendEnter(fullName string) {
	tmuxBin, err := FindTmux()
	if err != nil {
		return
	}
	exec.Command(tmuxBin, "send-keys", "-t", fullName, "Enter").Run() //nolint:errcheck
}

// SendLiteral sends literal text to a session WITHOUT pressing Enter.
// Used for the orchestrator notification path — the message should land in
// the session's chat input so the user can review and edit it before
// submitting, not be auto-sent.
func SendLiteral(fullName, text string) error {
	tmuxBin, err := FindTmux()
	if err != nil {
		return err
	}
	cmd := exec.Command(tmuxBin, "send-keys", "-t", fullName, "-l", text)
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

// ReadHistoryTail reads the last n lines from ~/.claude/history.jsonl.
func ReadHistoryTail(n int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := home + "/.claude/history.jsonl"

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Read from end: seek to end, then scan backwards for n newlines
	stat, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := stat.Size()
	if size == 0 {
		return "", nil
	}

	// Read up to 64KB from the end (should be plenty for ~100 lines)
	readSize := int64(64 * 1024)
	if readSize > size {
		readSize = size
	}
	buf := make([]byte, readSize)
	_, err = f.ReadAt(buf, size-readSize)
	if err != nil {
		return "", err
	}

	content := string(buf)
	lines := strings.Split(content, "\n")

	// Take last n non-empty lines
	var result []string
	for i := len(lines) - 1; i >= 0 && len(result) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			result = append([]string{lines[i]}, result...)
		}
	}
	return strings.Join(result, "\n"), nil
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
