package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/simon/crabctl/internal/tmux"
)

type Status int

const (
	Unknown    Status = iota
	Running           // actively working
	Waiting           // at prompt, idle
	Permission        // waiting for user permission
)

func (s Status) String() string {
	switch s {
	case Running:
		return "running"
	case Waiting:
		return "waiting"
	case Permission:
		return "permission"
	default:
		return "unknown"
	}
}

type Session struct {
	Name          string
	FullName      string
	Status        Status
	Mode          string // "bypass", "plan", "", etc.
	LastAction    string // e.g. "Write(/tmp/foo.txt)", "Done."
	Duration      time.Duration
	AttachedCount int
	WorkDir       string
}

// List returns all crab-* sessions with status detection.
func List() ([]Session, error) {
	infos, err := tmux.ListSessions()
	if err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(infos))
	for _, info := range infos {
		output, _ := tmux.CapturePaneOutput(info.FullName, 25)
		status, mode, lastAction := analyzeOutput(output)
		workDir := tmux.GetPanePath(info.FullName)

		sessions = append(sessions, Session{
			Name:          info.Name,
			FullName:      info.FullName,
			Status:        status,
			Mode:          mode,
			LastAction:    lastAction,
			Duration:      time.Since(info.Created),
			AttachedCount: info.AttachedCount,
			WorkDir:       workDir,
		})
	}
	return sessions, nil
}

// analyzeOutput extracts status, mode, and last action from captured pane output.
func analyzeOutput(output string) (Status, string, string) {
	if output == "" {
		return Unknown, "", ""
	}

	lines := strings.Split(output, "\n")

	// Detect mode from the status bar line (bottom area)
	mode := detectMode(lines)

	// Detect last action (most recent ⏺ line)
	lastAction := detectLastAction(lines)

	// Detect status
	status := detectStatus(lines)

	return status, mode, lastAction
}

func detectStatus(lines []string) Status {
	// Strategy: scan bottom-up for the most relevant indicator.
	// The status bar at the very bottom tells us the most.

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// "esc to interrupt" in status bar = actively running
		if strings.Contains(trimmed, "esc to interrupt") {
			return Running
		}

		// Permission prompt indicators
		if isPermissionLine(trimmed) {
			return Permission
		}
	}

	// Check for running indicators: ✽/✻ (thinking/doing spinners)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "✽") || strings.HasPrefix(trimmed, "✻") {
			return Running
		}
		// Braille spinner characters
		if strings.ContainsAny(trimmed, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
			return Running
		}
	}

	// Check for idle prompt: bare ❯ on its own line (with nothing after it,
	// or just whitespace), appearing near the bottom
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		// Skip the status/mode bar lines
		if strings.Contains(trimmed, "bypass permissions") ||
			strings.Contains(trimmed, "shift+tab") ||
			strings.Contains(trimmed, "auto-accept") ||
			strings.Contains(trimmed, "plan mode") ||
			strings.HasPrefix(trimmed, "───") ||
			strings.HasPrefix(trimmed, "╭") ||
			strings.HasPrefix(trimmed, "╰") ||
			strings.HasPrefix(trimmed, "│") {
			continue
		}
		// Bare prompt = waiting
		// Note: Claude uses \u00a0 (non-breaking space) after ❯
		if trimmed == "❯" || trimmed == ">" {
			return Waiting
		}
		if strings.HasPrefix(trimmed, "❯") {
			return Waiting
		}
		// If we hit a non-prompt, non-decoration line, stop looking
		break
	}

	return Unknown
}

func isPermissionLine(line string) bool {
	lower := strings.ToLower(line)
	// Claude's permission prompts
	if strings.Contains(lower, "allow") && strings.Contains(lower, "deny") {
		return true
	}
	if strings.Contains(lower, "yes / no") || strings.Contains(lower, "yes/no") {
		return true
	}
	// Tool approval prompts
	if strings.Contains(lower, "allow once") || strings.Contains(lower, "allow always") {
		return true
	}
	return false
}

func detectMode(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if strings.Contains(lower, "bypass permissions on") {
			return "bypass"
		}
		if strings.Contains(lower, "plan mode") {
			return "plan"
		}
		if strings.Contains(lower, "auto-accept edits") {
			return "auto-edit"
		}
	}
	return ""
}

func detectLastAction(lines []string) string {
	// Scan bottom-up for the most recent ⏺ line
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "⏺") {
			action := strings.TrimSpace(strings.TrimPrefix(trimmed, "⏺"))
			// Truncate long actions
			if len(action) > 40 {
				action = action[:37] + "..."
			}
			return action
		}
	}
	return ""
}

// Send sends text to a session.
func Send(fullName, text string) error {
	return tmux.SendKeys(fullName, text)
}

// Kill kills a session (Ctrl-C + kill).
func Kill(fullName string) error {
	return tmux.KillSession(fullName)
}

// FormatDuration formats a duration for display.
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, hours)
}
