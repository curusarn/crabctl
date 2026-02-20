package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ClaudeSession represents a past Claude Code conversation that can be resumed.
type ClaudeSession struct {
	UUID         string
	ProjectDir   string // working directory from session, or decoded from dir name
	ModTime      time.Time
	FirstMessage string // first user message, truncated
	encodedDir   string // internal: encoded dir name for file lookup
}

// ListRecentClaudeSessions scans ~/.claude/projects/ for recent session files.
// Returns up to limit sessions sorted by most recently modified first.
func ListRecentClaudeSessions(limit int) []ClaudeSession {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	projectsDir := filepath.Join(home, ".claude", "projects")

	projectDirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var all []ClaudeSession
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		dirPath := filepath.Join(projectsDir, pd.Name())
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			uuid := strings.TrimSuffix(e.Name(), ".jsonl")
			all = append(all, ClaudeSession{
				UUID:       uuid,
				ProjectDir: pd.Name(), // placeholder, replaced by readSessionMeta
				ModTime:    info.ModTime(),
				encodedDir: pd.Name(),
			})
		}
	}

	// Sort by most recent first
	sort.Slice(all, func(i, j int) bool {
		return all[i].ModTime.After(all[j].ModTime)
	})

	if len(all) > limit {
		all = all[:limit]
	}

	// Read metadata (cwd + first message) for the top results
	for i := range all {
		meta := readSessionMeta(
			filepath.Join(projectsDir, all[i].encodedDir, all[i].UUID+".jsonl"),
		)
		all[i].FirstMessage = meta.FirstMessage
		if meta.CWD != "" {
			all[i].ProjectDir = meta.CWD
		}
	}

	return all
}

// sessionMeta holds the cwd, first message, and start time extracted from a JSONL file.
type sessionMeta struct {
	CWD          string
	FirstMessage string
	Started      time.Time // timestamp of the first user message
}

// readSessionMeta reads the cwd, first user message, and start time from a JSONL session file.
func readSessionMeta(path string) sessionMeta {
	f, err := os.Open(path)
	if err != nil {
		return sessionMeta{}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	var meta sessionMeta
	for scanner.Scan() {
		var msg struct {
			Type      string `json:"type"`
			CWD       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type != "user" {
			continue
		}

		if meta.CWD == "" && msg.CWD != "" {
			meta.CWD = msg.CWD
		}

		if meta.Started.IsZero() && msg.Timestamp != "" {
			meta.Started, _ = time.Parse(time.RFC3339Nano, msg.Timestamp)
		}

		if meta.FirstMessage != "" {
			continue
		}

		content := extractContent(msg.Message.Content)
		if content == "" {
			continue
		}

		// Skip skill/command invocations
		if strings.HasPrefix(content, "<command-message>") {
			continue
		}

		content = strings.ReplaceAll(content, "\n", " ")
		if len(content) > 80 {
			content = content[:77] + "..."
		}
		meta.FirstMessage = content

		// Got all three, stop reading
		if meta.CWD != "" && !meta.Started.IsZero() {
			break
		}
	}
	return meta
}

// extractContent gets the text from a JSONL content field (string or array).
func extractContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try string first
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try array of content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}

	return ""
}

// FindSessionUUID finds the Claude session file for a given workDir that was
// created closest to (and shortly after) sessionStart. This correctly identifies
// which session file belongs to a specific tmux session, even when multiple
// Claude sessions share the same workdir.
// Falls back to most recently modified file if no timestamp match is found.
func FindSessionUUID(workDir string, sessionStart time.Time) (uuid string, firstMsg string) {
	if workDir == "" {
		return "", ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", ""
	}

	encoded := encodeProjectDir(workDir)
	projectDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", ""
	}

	type candidate struct {
		uuid     string
		firstMsg string
		started  time.Time
		modTime  time.Time
	}

	var candidates []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		u := strings.TrimSuffix(e.Name(), ".jsonl")
		meta := readSessionMeta(filepath.Join(projectDir, u+".jsonl"))
		candidates = append(candidates, candidate{
			uuid:     u,
			firstMsg: meta.FirstMessage,
			started:  meta.Started,
			modTime:  info.ModTime(),
		})
	}

	if len(candidates) == 0 {
		return "", ""
	}

	// Find the session whose first message timestamp is closest to sessionStart
	// (must be within 2 minutes after sessionStart)
	if !sessionStart.IsZero() {
		var bestMatch *candidate
		bestDiff := 2 * time.Minute
		for i := range candidates {
			c := &candidates[i]
			if c.started.IsZero() {
				continue
			}
			diff := c.started.Sub(sessionStart)
			if diff >= 0 && diff < bestDiff {
				bestDiff = diff
				bestMatch = c
			}
		}
		if bestMatch != nil {
			return bestMatch.uuid, bestMatch.firstMsg
		}
	}

	// Fallback: most recently modified file
	var best *candidate
	for i := range candidates {
		if best == nil || candidates[i].modTime.After(best.modTime) {
			best = &candidates[i]
		}
	}
	if best != nil {
		return best.uuid, best.firstMsg
	}
	return "", ""
}

// encodeProjectDir encodes a directory path the same way Claude Code does
// for its project session storage: slashes become hyphens, leading slash
// is included (so /Users/foo becomes -Users-foo).
func encodeProjectDir(dir string) string {
	return strings.ReplaceAll(dir, "/", "-")
}

// findLatestSessionFile finds the most recently modified .jsonl file
// in Claude's project session directory for the given workDir.
// Returns zero time if no files found.
func findLatestSessionFile(workDir string) time.Time {
	if workDir == "" {
		return time.Time{}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return time.Time{}
	}

	encoded := encodeProjectDir(workDir)
	projectDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return time.Time{}
	}

	var latest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}

	return latest
}
