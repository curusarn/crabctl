package tmux

import (
	"os"
	"os/exec"
	"strings"
)

// DetectParent determines the parent session key for a new session.
// Priority: 1) explicit flag, 2) tmux session name (if crab-*), 3) CRABCTL_NAME env var.
func DetectParent(explicit string) string {
	if explicit != "" {
		return explicit
	}

	// Check if we're inside a crab tmux session. Target our own pane
	// explicitly ($TMUX_PANE): an untargeted display-message from a
	// tty-less client (e.g. Claude Code's Bash tool, stdio all pipes)
	// makes the server guess the client's pane, and when the guess fails
	// it silently falls back to the most recently ACTIVE session, which
	// is how children got recorded as siblings under whatever crab
	// happened to be busiest. Never query without a target.
	if os.Getenv("TMUX") != "" {
		if pane := os.Getenv("TMUX_PANE"); pane != "" {
			tmuxBin, err := FindTmux()
			if err == nil {
				cmd := exec.Command(tmuxBin, "display-message", "-p", "-t", pane, "#{session_name}")
				out, err := cmd.Output()
				if err == nil {
					name := strings.TrimSpace(string(out))
					if strings.HasPrefix(name, SessionPrefix) {
						return name
					}
				}
			}
		}
	}

	// Fallback to CRABCTL_NAME env var (for non-tmuxed orchestrators)
	if name := os.Getenv("CRABCTL_NAME"); name != "" {
		return name
	}

	return ""
}
