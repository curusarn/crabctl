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
	Confirm           // plan approval or other confirmation dialog
	TaskDone          // agent reported task completion
)

func (s Status) String() string {
	switch s {
	case Running:
		return "running"
	case Waiting:
		return "waiting"
	case Permission:
		return "permission"
	case Confirm:
		return "confirm"
	case TaskDone:
		return "task done"
	default:
		return "unknown"
	}
}

type Session struct {
	Name          string
	FullName      string
	Host          string // empty for local, nickname for remote
	Status        Status
	Mode          string // "bypass", "plan", "", etc.
	LastAction    string // e.g. "Write(/tmp/foo.txt)", "Done."
	GitChanges    string // e.g. "5 files +415 -44"
	PR            string // e.g. "PR #498"
	Context       string // e.g. "10%" (context remaining)
	Duration      time.Duration
	LastActive    time.Time // most recent Claude session file mtime
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
		status, bar, lastAction := analyzeOutput(output)
		workDir := tmux.GetPanePath(info.FullName)

		sessions = append(sessions, Session{
			Name:          info.Name,
			FullName:      info.FullName,
			Status:        status,
			Mode:          bar.Mode,
			LastAction:    lastAction,
			GitChanges:    bar.GitChanges,
			PR:            bar.PR,
			Context:       bar.Context,
			Duration:      time.Since(info.Created),
			LastActive:    findLatestSessionFile(workDir),
			AttachedCount: info.AttachedCount,
			WorkDir:       workDir,
		})
	}
	SortSessions(sessions)
	return sessions, nil
}

// ListExecutor returns sessions from a single executor.
func ListExecutor(ex tmux.Executor) ([]Session, error) {
	host := ex.HostName()

	infos, err := ex.ListSessions()
	if err != nil {
		return nil, err
	}

	sessions := make([]Session, 0, len(infos))
	for _, info := range infos {
		output, _ := ex.CapturePaneOutput(info.FullName, 25)
		status, bar, lastAction := analyzeOutput(output)
		workDir := ex.GetPanePath(info.FullName)

		var lastActive time.Time
		if host == "" {
			lastActive = findLatestSessionFile(workDir)
		}

		sessions = append(sessions, Session{
			Name:          info.Name,
			FullName:      info.FullName,
			Host:          host,
			Status:        status,
			Mode:          bar.Mode,
			LastAction:    lastAction,
			GitChanges:    bar.GitChanges,
			PR:            bar.PR,
			Context:       bar.Context,
			Duration:      time.Since(info.Created),
			LastActive:    lastActive,
			AttachedCount: info.AttachedCount,
			WorkDir:       workDir,
		})
	}
	return sessions, nil
}

// statusPriority returns sort priority (lower = more important, shown first).
func statusPriority(s Status) int {
	switch s {
	case Permission, Confirm, TaskDone:
		return 0
	case Running:
		return 1
	case Waiting:
		return 2
	default:
		return 3
	}
}

// SortSessions sorts by: local first (by status priority, then duration),
// remote after (by status priority, then duration).
func SortSessions(sessions []Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		iLocal := sessions[i].Host == ""
		jLocal := sessions[j].Host == ""
		if iLocal != jLocal {
			return iLocal // local before remote
		}
		pi, pj := statusPriority(sessions[i].Status), statusPriority(sessions[j].Status)
		if pi != pj {
			return pi < pj
		}
		return sessions[i].Duration < sessions[j].Duration
	})
}

// DetectStatus returns the session status from raw pane output.
func DetectStatus(output string) Status {
	if output == "" {
		return Unknown
	}
	return detectStatus(strings.Split(output, "\n"))
}

type statusBarInfo struct {
	Mode       string
	GitChanges string
	PR         string
	Context    string
}

// analyzeOutput extracts status, mode, last action, and status bar info from captured pane output.
func analyzeOutput(output string) (Status, statusBarInfo, string) {
	if output == "" {
		return Unknown, statusBarInfo{}, ""
	}

	lines := strings.Split(output, "\n")

	// Parse the bottom status bar for mode and metadata
	bar := parseStatusBar(lines)

	// Detect last action (most recent ⏺ line)
	lastAction := detectLastAction(lines)

	// Detect status
	status := detectStatus(lines)

	return status, bar, lastAction
}

func detectStatus(lines []string) Status {
	// Scan bottom-up for status indicators near the bottom of the screen.
	// Check up to 10 non-decoration content lines to handle cases where
	// UI elements (plan approval menus, selection items) appear between
	// the prompt and the bottom of the screen.
	// All checks are bottom-up only to avoid false positives from
	// conversation content that happens to contain matching text.
	contentLines := 0
	sawNumberedMenu := false
	sawPrompt := false
	for i := len(lines) - 1; i >= 0 && contentLines < 10; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if isDecorationLine(trimmed) {
			// "esc to interrupt" appears in the decoration/status bar area
			if strings.Contains(trimmed, "esc to interrupt") {
				return Running
			}
			// ╌ dashed line after seeing numbered menu = plan confirmation
			if sawNumberedMenu && strings.HasPrefix(trimmed, "╌") {
				return Confirm
			}
			continue
		}
		contentLines++

		// Once we've seen the prompt, only scan for TASK DONE!
		if sawPrompt {
			if strings.Contains(trimmed, "TASK DONE!") {
				return TaskDone
			}
			// Non-TASK-DONE content above prompt = just waiting
			return Waiting
		}

		// Permission prompt near the bottom
		if isPermissionLine(trimmed) {
			return Permission
		}
		// Numbered menu items (plan approval: "1. Yes, ...", "❯ 1. Yes, ...")
		if isNumberedMenuItem(trimmed) {
			sawNumberedMenu = true
			continue
		}
		// Bare prompt — note it but keep scanning for TASK DONE! above
		// Note: Claude uses \u00a0 (non-breaking space) after ❯
		if trimmed == "❯" || trimmed == ">" || strings.HasPrefix(trimmed, "❯") {
			sawPrompt = true
			continue
		}
		// Active progress indicator near the bottom = running.
		// Structural detection: a line containing "…" (U+2026 ellipsis) that
		// isn't a truncation indicator ("… +N lines") or action marker ("⏺").
		// This matches any spinner character + verb pattern like:
		//   "✻ Thinking…", "✽ Transfiguring… (2m 22s)", "✳ Blanching…"
		// regardless of which specific spinner character Claude Code uses.
		if isRunningIndicator(trimmed) {
			return Running
		}
	}

	if sawPrompt {
		return Waiting
	}
	return Unknown
}

// isRunningIndicator detects Claude Code's active progress lines.
// Matches structural patterns rather than specific spinner characters:
//   - Lines containing "…" (ellipsis) that aren't truncation or action markers
//   - Lines containing braille spinner characters (⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏)
func isRunningIndicator(trimmed string) bool {
	// Braille spinner characters (used in various loading states)
	if strings.ContainsAny(trimmed, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
		return true
	}

	// Must contain "…" (U+2026 ellipsis) for verb-based spinners
	if !strings.Contains(trimmed, "…") {
		return false
	}

	// Exclude truncation indicators: "… +30 lines (ctrl+o to expand)"
	if strings.HasPrefix(trimmed, "…") {
		return false
	}

	// Exclude action markers (completed tool calls)
	if strings.HasPrefix(trimmed, "⏺") {
		return false
	}

	// Exclude prompt lines
	if strings.HasPrefix(trimmed, "❯") || trimmed == ">" {
		return false
	}

	// Exclude indented continuation/collapse lines: "     … +4 lines"
	if strings.Contains(trimmed, "… +") && strings.Contains(trimmed, "lines") {
		return false
	}

	// Exclude the "Waiting…" text that appears in tool output
	if trimmed == "Waiting…" {
		return false
	}

	return true
}

func isDecorationLine(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "bypass permissions on") ||
		strings.Contains(lower, "shift+tab") ||
		strings.Contains(lower, "auto-accept") ||
		strings.Contains(lower, "accept edits on") ||
		strings.Contains(lower, "plan mode on") ||
		strings.Contains(lower, "for shortcuts") ||
		strings.Contains(lower, "esc to interrupt") ||
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

// isNumberedMenuItem detects plan approval menu items like:
//
//	"❯ 1. Yes, clear context and bypass permissions"
//	"2. Yes, and bypass permissions"
//	"4. Type here to tell Claude what to change"
func isNumberedMenuItem(trimmed string) bool {
	s := trimmed
	// Strip leading ❯ selector
	s = strings.TrimPrefix(s, "❯")
	s = strings.TrimSpace(s)
	// Check for "N. " pattern
	if len(s) >= 3 && s[0] >= '1' && s[0] <= '9' && s[1] == '.' && s[2] == ' ' {
		return true
	}
	return false
}

// parseStatusBar extracts mode and metadata from the bottom status bar.
// The bar format is segments separated by " · ", e.g.:
//
//	⏵⏵ bypass permissions on (shift+tab to cycle) · 5 files +415 -44 · PR #498
//	? for shortcuts                                     Context left until auto-compact: 10%
func parseStatusBar(lines []string) statusBarInfo {
	var bar statusBarInfo

	// Find the status bar line(s) — scan bottom-up, collect decoration lines
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if !isDecorationLine(trimmed) {
			break
		}

		lower := strings.ToLower(trimmed)

		// Mode detection
		if bar.Mode == "" {
			if strings.Contains(lower, "bypass permissions on") {
				bar.Mode = "bypass"
			} else if strings.Contains(lower, "plan mode") {
				bar.Mode = "plan"
			} else if strings.Contains(lower, "auto-accept edits") || strings.Contains(lower, "accept edits on") {
				bar.Mode = "auto-edit"
			}
		}

		// Context warning: "Context left until auto-compact: 10%"
		if idx := strings.Index(lower, "context left until auto-compact:"); idx >= 0 {
			rest := strings.TrimSpace(trimmed[idx+len("context left until auto-compact:"):])
			bar.Context = rest
		}

		// Split by " · " to parse segments
		segments := strings.Split(trimmed, " · ")
		for _, seg := range segments {
			seg = strings.TrimSpace(seg)
			segLower := strings.ToLower(seg)

			// PR reference: "PR #123"
			if strings.HasPrefix(segLower, "pr #") {
				bar.PR = seg
			}

			// Git changes: "5 files +415 -44" or "1 file +1185 -515"
			if strings.Contains(segLower, "file") && strings.Contains(seg, "+") {
				bar.GitChanges = seg
			}
		}
	}

	return bar
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

// FormatDurationCoarse formats a duration using only the largest unit.
func FormatDurationCoarse(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours())/24)
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
