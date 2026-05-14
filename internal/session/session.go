package session

import (
	"fmt"
	"sort"
	"strings"
	"sync"
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
	Name            string
	FullName        string
	Host            string // empty for local, nickname for remote
	Status          Status
	Mode            string // "bypass", "plan", "", etc.
	LastAction      string // e.g. "Write(/tmp/foo.txt)", "Done."
	GitChanges      string // e.g. "5 files +415 -44"
	PR              string // e.g. "PR #498"
	PRURL           string // e.g. "https://github.com/owner/repo/pull/498"
	PRState         string // "open", "draft", "merged", "closed"
	Context         string // e.g. "10%" (context remaining)
	Duration        time.Duration
	LastActive      time.Time // most recent Claude session file mtime
	AttachedCount   int
	WorkDir         string
	PaneContent     string // latest captured pane output (for UUID matching)
	SessionUUID     string // matched Claude session file UUID
	SessionFirstMsg string // first user message from matched session
	Parent          string // parent session's FullName (e.g. "crab-orchestrator")
	TreeDepth       int    // 0=top-level, 1=child, 2=grandchild+
	TreePrefix      string // "├── ", "└── ", etc.
	Virtual         bool   // true = placeholder parent with no tmux session
	HiddenCount     int    // number of hidden descendants (computed by BuildTree)
	TreeHidden      bool   // true = hidden by fold state, excluded from display
}

// prCacheEntry holds a cached PR lookup result.
type prCacheEntry struct {
	PR, PRURL, PRState string
	ResolvedAt         time.Time
	Persistent         bool // true = loaded from DB, survives TTL expiry
}

var (
	prCache   = make(map[string]prCacheEntry) // key: "host:fullName"
	prCacheMu sync.Mutex
)

const prCacheTTL = 5 * time.Minute

// ClearPRCache removes all cached PR lookup results, forcing re-resolution.
func ClearPRCache() {
	prCacheMu.Lock()
	prCache = make(map[string]prCacheEntry)
	prCacheMu.Unlock()
}

// ResolveBranchPR returns the PR text, URL, and state for a session's branch via gh CLI.
// Results are cached for 5 minutes to avoid running gh on every tick.
func ResolveBranchPR(host, fullName, workDir string, ex tmux.Executor) (string, string, string) {
	if workDir == "" {
		return "", "", ""
	}
	key := SessionKey(host, fullName)

	prCacheMu.Lock()
	if entry, ok := prCache[key]; ok {
		if entry.Persistent || time.Since(entry.ResolvedAt) < prCacheTTL {
			prCacheMu.Unlock()
			return entry.PR, entry.PRURL, entry.PRState
		}
	}
	prCacheMu.Unlock()

	pr, prURL, prState := ex.GetBranchPR(workDir)

	prCacheMu.Lock()
	prCache[key] = prCacheEntry{PR: pr, PRURL: prURL, PRState: prState, ResolvedAt: time.Now()}
	prCacheMu.Unlock()

	return pr, prURL, prState
}

// WarmPRCache populates the PR cache from DB-stored PR URLs.
// Keys are in SessionKey format (e.g. "crab-worker" or "bay1:crab-worker").
// Entries are marked persistent so they survive TTL expiry.
func WarmPRCache(entries map[string][2]string) {
	prCacheMu.Lock()
	defer prCacheMu.Unlock()
	for key, data := range entries {
		prURL, prState := data[0], data[1]
		// Extract "PR #N" from URL (last path segment)
		pr := ""
		if idx := strings.LastIndex(prURL, "/"); idx >= 0 {
			pr = "PR #" + prURL[idx+1:]
		}
		prCache[key] = prCacheEntry{
			PR:         pr,
			PRURL:      prURL,
			PRState:    prState,
			ResolvedAt: time.Now(),
			Persistent: true,
		}
	}
}

// List returns all crab-* sessions with status detection.
func List() ([]Session, error) {
	return ListExecutor(&tmux.LocalExecutor{})
}

// ListExecutor returns sessions from a single executor.
// PR resolution is NOT done here — it's handled lazily by the TUI
// to avoid blocking session list display on slow gh CLI calls.
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

		// Use PR info from Claude Code's status bar (instant).
		// Full URL is resolved lazily by the TUI via ResolveBranchPR.
		pr := bar.PR
		prURL := ""

		// Apply cached PR if available (no network call)
		prState := ""
		if cachedPR, cachedURL, cachedState, ok := LookupCachedPR(host, info.FullName); ok {
			pr = cachedPR
			prURL = cachedURL
			prState = cachedState
		}

		sessions = append(sessions, Session{
			Name:          info.Name,
			FullName:      info.FullName,
			Host:          host,
			Status:        status,
			Mode:          bar.Mode,
			LastAction:    lastAction,
			GitChanges:    bar.GitChanges,
			PR:            pr,
			PRURL:         prURL,
			PRState:       prState,
			Context:       bar.Context,
			Duration:      time.Since(info.Created),
			AttachedCount: info.AttachedCount,
			WorkDir:       workDir,
			PaneContent:   output,
			Parent:        info.Parent,
		})
	}
	return sessions, nil
}

// LookupCachedPR returns PR info from cache without any network calls.
// Persistent entries (loaded from DB) survive TTL expiry.
func LookupCachedPR(host, fullName string) (string, string, string, bool) {
	key := SessionKey(host, fullName)
	prCacheMu.Lock()
	defer prCacheMu.Unlock()
	entry, ok := prCache[key]
	if !ok {
		return "", "", "", false
	}
	if !entry.Persistent && time.Since(entry.ResolvedAt) >= prCacheTTL {
		return "", "", "", false
	}
	return entry.PR, entry.PRURL, entry.PRState, true
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
	//
	// Claude Code's TUI layout (bottom to top):
	//   status bar (bypass permissions, etc.)
	//   ─── separator
	//   prompt line (❯) or interaction area
	//   ─── separator
	//   chat content
	//
	// We track ─── separators to distinguish structural footer lines from
	// actual content. Only lines above the first ─── count toward the
	// content line limit (10 lines), preventing unrecognized footer
	// elements from burning the scan budget. Detection checks (prompt,
	// running, permission, confirm) run on all non-decoration lines
	// regardless of separator count.
	contentLines := 0
	separatorsSeen := 0
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
			// ─── lines are structural separators in Claude's TUI layout
			if strings.HasPrefix(trimmed, "───") {
				separatorsSeen++
			}
			continue
		}

		// Only count toward content limit after crossing the first ───
		// separator. Lines below it (status bar, footer decorations) are
		// structural and shouldn't consume the scan budget.
		if separatorsSeen > 0 {
			contentLines++
		}

		// Once we've seen the prompt, check for TASK DONE! and running
		// indicators above the prompt before concluding "waiting".
		// Note: the autoforward prompt contains TASK_DONE! (underscore) to
		// avoid false positives from the sent message visible in the pane.
		if sawPrompt {
			if strings.Contains(trimmed, "TASK DONE!") {
				return TaskDone
			}
			// A spinner line above the prompt means the session is
			// actively running — the ❯ prompt is just Claude Code's
			// TUI layout, not an idle indicator.
			if isRunningIndicator(trimmed) {
				return Running
			}
			// Non-TASK-DONE, non-running content above prompt = just waiting
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
	if sawNumberedMenu {
		return Confirm
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
		strings.Contains(lower, "ctrl-g to edit") ||
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
