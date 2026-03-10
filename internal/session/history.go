package session

import (
	"encoding/json"
	"strings"
	"time"
)

// historyEntry represents a single line from ~/.claude/history.jsonl.
type historyEntry struct {
	Display   string `json:"display"`
	SessionID string `json:"sessionId"`
	Project   string `json:"project"`
	Timestamp int64  `json:"timestamp"`
}

// FindSessionIDByMessage reads history lines (newest last) and returns the
// sessionId of the most recent entry whose "display" field contains the
// given message text AND was written recently (within maxAge).
// This handles the case where many sessions share the same workdir and
// generic messages like "continue" appear frequently.
func FindSessionIDByMessage(historyLines string, message string, maxAge time.Duration, workDir string) string {
	now := time.Now().UnixMilli()
	cutoff := now - maxAge.Milliseconds()

	lines := strings.Split(strings.TrimSpace(historyLines), "\n")
	// Scan from end (most recent first)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry historyEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		// Skip entries older than cutoff
		if entry.Timestamp > 0 && entry.Timestamp < cutoff {
			continue
		}
		// When workDir is set, require project match to avoid cross-session confusion
		if workDir != "" && entry.Project != "" && entry.Project != workDir {
			continue
		}
		if entry.SessionID != "" && entry.Display != "" && strings.Contains(entry.Display, message) {
			return entry.SessionID
		}
	}
	return ""
}

// FindSessionIDByPaneContent reads history lines and returns the sessionId
// with the most "display" entries visible in the given pane output.
// No project filtering needed — the pane content is ground truth showing
// exactly which conversation is on screen. Used after detach.
func FindSessionIDByPaneContent(historyLines string, paneContent string) string {
	if paneContent == "" {
		return ""
	}

	lines := strings.Split(strings.TrimSpace(historyLines), "\n")

	// Count how many display entries per sessionId appear in pane content,
	// and track the most recent timestamp per session for tiebreaking.
	scores := make(map[string]int)
	latestTS := make(map[string]int64)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry historyEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.SessionID == "" {
			continue
		}
		// Skip very short display text — too likely to false-match
		display := strings.TrimSpace(entry.Display)
		if len(display) < 8 {
			continue
		}
		if strings.Contains(paneContent, display) {
			scores[entry.SessionID]++
			if entry.Timestamp > latestTS[entry.SessionID] {
				latestTS[entry.SessionID] = entry.Timestamp
			}
		}
	}

	if len(scores) == 0 {
		return ""
	}

	// Return sessionId with highest score (>= 3 required),
	// break ties by most recent timestamp.
	bestID := ""
	bestScore := 0
	var bestTS int64
	for id, score := range scores {
		if score > bestScore || (score == bestScore && latestTS[id] > bestTS) {
			bestScore = score
			bestID = id
			bestTS = latestTS[id]
		}
	}
	if bestScore < 3 {
		return ""
	}
	return bestID
}
