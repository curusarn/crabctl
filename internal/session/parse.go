package session

import (
	"strings"
)

// PaneState describes the structurally-parsed state of a Claude Code pane.
//
// State detection looks at the bottom-most pair of horizontal-rule lines
// (long runs of U+2500 ─) which bound Claude Code's input prompt box. The
// content between those rules is the input buffer; the lines below the lower
// rule are the status bar. Permission/running/errored signals override the
// idle/queued reading from the input box.
type PaneState struct {
	State        string `json:"state"` // idle | queued | running | permission | errored | unknown
	InputBuffer  string `json:"input_buffer"`
	LastToolLine string `json:"last_tool_line"`
	StatusBar    string `json:"status_bar"`
}

// ParsePane analyzes a (ANSI-stripped, ghost-stripped) pane capture and
// returns its structured state. The raw input is the same text that
// `crabctl capture` already prints today.
func ParsePane(raw string) PaneState {
	s := PaneState{State: "unknown"}
	if strings.TrimSpace(raw) == "" {
		return s
	}

	rawLines := strings.Split(raw, "\n")
	// Drop trailing empty lines so the "bottom" we scan is meaningful.
	lines := trimTrailingEmpty(rawLines)
	if len(lines) == 0 {
		return s
	}

	// Locate the bottom-most pair of ─── rules. They form the input prompt box.
	upper, lower := findInputBoxRules(lines)

	// Status bar: lines below the lower rule (or below the only rule, if no
	// pair was found). Skip blank lines.
	s.StatusBar = collectStatusBar(lines, lower)

	s.LastToolLine = findLastToolLine(lines)

	// Permission detection runs against the whole pane — the prompt box may
	// be replaced by a confirmation dialog when Claude is waiting for input.
	if isPermissionPane(lines) {
		s.State = "permission"
		// When in permission, also extract input buffer if any (rare).
		s.InputBuffer = extractInputBuffer(lines, upper, lower)
		return s
	}

	// Running overrides idle/queued: the prompt may still render empty
	// while Claude is processing.
	if isRunningStatusBar(s.StatusBar) {
		s.State = "running"
		s.InputBuffer = extractInputBuffer(lines, upper, lower)
		return s
	}

	// Errored: surface obvious error markers in conversation.
	if isErroredPane(lines) {
		s.State = "errored"
		s.InputBuffer = extractInputBuffer(lines, upper, lower)
		return s
	}

	// Idle vs queued from the input prompt box.
	if upper >= 0 && lower > upper {
		buf := extractInputBuffer(lines, upper, lower)
		s.InputBuffer = buf
		if buf == "" {
			s.State = "idle"
		} else {
			s.State = "queued"
		}
		return s
	}

	// No pair of rules — be conservative.
	return s
}

// findInputBoxRules returns the line indices of the upper and lower rule
// of the bottom-most input prompt box. Returns (-1, -1) if no pair is
// found.
//
// Strategy: scan bottom-up, collect indices of rule lines, and pick the
// bottom-most adjacent pair (no other rule between them). The pair must
// be reasonably close to the bottom — past structural noise we'd otherwise
// match conversation-area rules.
func findInputBoxRules(lines []string) (int, int) {
	var rules []int
	for i := len(lines) - 1; i >= 0; i-- {
		if isHorizontalRule(lines[i]) {
			rules = append(rules, i)
		}
	}
	if len(rules) < 2 {
		if len(rules) == 1 {
			return -1, rules[0]
		}
		return -1, -1
	}
	// rules is bottom-up; rules[0] is the bottom-most rule (lower bound),
	// rules[1] is the next one above (upper bound).
	lower := rules[0]
	upper := rules[1]
	// Sanity: input buffers are at most ~6 lines tall in practice; if the
	// gap is huge, this is probably not the prompt box but a divider in
	// the conversation. Keep the lower as an isolated rule.
	if lower-upper > 10 {
		return -1, lower
	}
	return upper, lower
}

// extractInputBuffer returns the user-typed text between the two rules.
// Strips leading "❯ " and the non-breaking space Claude Code sometimes
// renders. Multi-line buffers are joined with "\n".
func extractInputBuffer(lines []string, upper, lower int) string {
	if upper < 0 || lower <= upper+1 {
		return ""
	}
	var parts []string
	for i := upper + 1; i < lower; i++ {
		parts = append(parts, lines[i])
	}
	if len(parts) == 0 {
		return ""
	}
	// Trim the leading "❯" from the first line.
	first := parts[0]
	first = strings.TrimLeft(first, " \t")
	first = strings.TrimPrefix(first, "❯")
	first = strings.TrimPrefix(first, ">")
	parts[0] = first

	// Join, then collapse trailing whitespace and NBSP ( ).
	joined := strings.Join(parts, "\n")
	// Strip leading whitespace including NBSP from the joined text.
	joined = strings.TrimLeft(joined, " \t ")
	joined = strings.TrimRight(joined, " \t \n")
	return joined
}

func collectStatusBar(lines []string, lowerRule int) string {
	if lowerRule < 0 {
		// Fall back to the last non-empty decoration line at the bottom.
		for i := len(lines) - 1; i >= 0; i-- {
			t := strings.TrimSpace(lines[i])
			if t == "" {
				continue
			}
			if isDecorationLine(t) && !isHorizontalRule(lines[i]) {
				return t
			}
			break
		}
		return ""
	}
	var out []string
	for i := lowerRule + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, " · ")
}

func findLastToolLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "⏺") {
			return t
		}
	}
	return ""
}

// isHorizontalRule matches the long runs of ─ Claude Code uses to bound
// the input prompt box. Permits leading/trailing whitespace.
func isHorizontalRule(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	// Need a meaningful run — at least 8 ─ characters in a row.
	count := 0
	for _, r := range t {
		if r == '─' {
			count++
			if count >= 8 {
				// Ensure the rest of the line is also rules / whitespace.
				return onlyHorizontalRule(t)
			}
		} else {
			return false
		}
	}
	return false
}

func onlyHorizontalRule(t string) bool {
	for _, r := range t {
		if r != '─' {
			return false
		}
	}
	return true
}

// isPermissionPane checks for any tool-approval or confirmation prompt
// blocking the agent. Covers Claude Code's two main forms:
//
//	(1) "Allow" / "Deny" / "Allow once" / "Allow always" buttons.
//	(2) Numbered menu like "❯ 1. Yes / 2. Yes, and always allow / 3. No"
//	    introduced by "Do you want to proceed?".
func isPermissionPane(lines []string) bool {
	hasYesNoMenu := false
	hasProceed := false
	hasAllowDeny := false
	hasAlwaysAllow := false
	for _, raw := range lines {
		l := strings.TrimSpace(raw)
		if l == "" {
			continue
		}
		lower := strings.ToLower(l)
		if strings.Contains(lower, "do you want to proceed") {
			hasProceed = true
		}
		if strings.Contains(lower, "always allow access") || strings.Contains(lower, "allow always") {
			hasAlwaysAllow = true
		}
		if strings.Contains(lower, "allow") && strings.Contains(lower, "deny") {
			hasAllowDeny = true
		}
		if strings.Contains(lower, "allow once") {
			hasAllowDeny = true
		}
		// "❯ 1. Yes" / "1. Yes" / "3. No" — match a numbered menu item with
		// Yes/No content.
		s := strings.TrimPrefix(l, "❯")
		s = strings.TrimSpace(s)
		if len(s) >= 4 && s[0] >= '1' && s[0] <= '9' && s[1] == '.' && s[2] == ' ' {
			rest := strings.ToLower(s[3:])
			if strings.HasPrefix(rest, "yes") || strings.HasPrefix(rest, "no") {
				hasYesNoMenu = true
			}
		}
	}
	return hasAllowDeny || hasAlwaysAllow || (hasProceed && hasYesNoMenu)
}

// isRunningStatusBar checks whether the status bar (lines below the
// input prompt box) signals that Claude is processing.
func isRunningStatusBar(bar string) bool {
	if bar == "" {
		return false
	}
	if strings.Contains(strings.ToLower(bar), "esc to interrupt") {
		return true
	}
	if strings.ContainsAny(bar, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏") {
		return true
	}
	return false
}

func isErroredPane(lines []string) bool {
	// Scan only the lower portion — we don't want stray "Error:" mentions
	// from earlier in the conversation to flip state.
	start := len(lines) - 15
	if start < 0 {
		start = 0
	}
	for i := start; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "Error:") {
			return true
		}
		if strings.Contains(l, "panic:") && strings.Contains(l, "goroutine") {
			return true
		}
	}
	return false
}

func trimTrailingEmpty(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}
