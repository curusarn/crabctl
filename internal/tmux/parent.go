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

	// Check if we're inside a crab tmux session.
	// Only trust tmux display-message when TMUX env is set — otherwise
	// it returns whichever session was most recently active, not ours.
	if os.Getenv("TMUX") != "" {
		tmuxBin, err := FindTmux()
		if err == nil {
			cmd := exec.Command(tmuxBin, "display-message", "-p", "#{session_name}")
			out, err := cmd.Output()
			if err == nil {
				name := strings.TrimSpace(string(out))
				if strings.HasPrefix(name, SessionPrefix) {
					return name
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
