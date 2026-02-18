package session

import (
	"fmt"
	"sort"
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
	sortSessions(sessions)
	return sessions, nil
}

// statusPriority returns sort priority (lower = more important, shown first).
func statusPriority(s Status) int {
	switch s {
	case Permission:
		return 0
	case Running:
		return 1
	case Waiting:
		return 2
	default:
		return 3
	}
}

// sortSessions sorts by status priority, then by duration (shortest first,
// meaning most recently created sessions appear first within each group).
func sortSessions(sessions []Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		pi, pj := statusPriority(sessions[i].Status), statusPriority(sessions[j].Status)
		if pi != pj {
			return pi < pj
		}
		return sessions[i].Duration < sessions[j].Duration
	})
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
	// 1. "esc to interrupt" anywhere = actively running (very specific string)
	for _, line := range lines {
		if strings.Contains(strings.TrimSpace(line), "esc to interrupt") {
			return Running
		}
	}

	// 2. Scan bottom-up for prompt, permission, or spinner near the bottom.
	// Check up to 10 non-decoration content lines to handle cases where
	// UI elements (plan approval menus, selection items) appear between
	// the prompt and the bottom of the screen.
	// Permission is checked here (not all-lines) to avoid false positives
	// from output content that happens to contain "allow" and "deny".
	contentLines := 0
	for i := len(lines) - 1; i >= 0 && contentLines < 10; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if isDecorationLine(trimmed) {
			continue
		}
		contentLines++
		// Permission prompt near the bottom
		if isPermissionLine(trimmed) {
			return Permission
		}
		// Bare prompt = waiting
		// Note: Claude uses \u00a0 (non-breaking space) after ❯
		if trimmed == "❯" || trimmed == ">" || strings.HasPrefix(trimmed, "❯") {
			return Waiting
		}
		// Active spinner near the bottom = running.
		// Active spinners have "…" (e.g. "✻ Thinking…"), completed ones don't
		// (e.g. "✻ Crunched for 5m 1s"). Only match active ones.
		if (strings.HasPrefix(trimmed, "✽") || strings.HasPrefix(trimmed, "✻") || strings.HasPrefix(trimmed, "✶")) &&
			strings.Contains(trimmed, "…") {
			return Running
		}
		if strings.ContainsAny(trimmed, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
			return Running
		}
	}

	return Unknown
}

func isDecorationLine(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "bypass permissions on") ||
		strings.Contains(lower, "shift+tab") ||
		strings.Contains(lower, "auto-accept") ||
		strings.Contains(lower, "plan mode on") ||
		strings.Contains(lower, "for shortcuts") ||
		strings.HasPrefix(trimmed, "───") ||
		strings.HasPrefix(trimmed, "╌") ||
		strings.HasPrefix(trimmed, "╭") ||
		strings.HasPrefix(trimmed, "╰") ||
		strings.HasPrefix(trimmed, "│")
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
