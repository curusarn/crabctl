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
	Name         string // crab session name (e.g. "crab-foo"), empty for non-crab sessions
	UUID         string
	ProjectDir   string // working directory from session, or decoded from dir name
	ModTime      time.Time
	FirstMessage string // first user message, truncated
	Killed       bool   // true if explicitly killed via crabctl (false = lost/crashed)
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

// FindSessionUUID finds the Claude session file for a given workDir.
// Uses multiple strategies: content matching against pane output (most reliable),
// timestamp matching, and modification time fallbacks.
// excludeUUIDs contains UUIDs already claimed by other sessions — these files
// are skipped entirely (not read from disk).
func FindSessionUUID(workDir string, sessionStart time.Time, paneContent string, excludeUUIDs map[string]bool) (uuid string, firstMsg string) {
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

	// Collect file info sorted newest-first so we read recent files first
	// and skip old unclaimed ones.
	type fileEntry struct {
		uuid    string
		modTime time.Time
	}
	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		u := strings.TrimSuffix(e.Name(), ".jsonl")
		if excludeUUIDs[u] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{uuid: u, modTime: info.ModTime()})
	}

	// Sort newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	// Only read metadata from the newest files (cap to avoid scanning
	// hundreds of old session files that will never match).
	const maxCandidates = 10
	if len(files) > maxCandidates {
		files = files[:maxCandidates]
	}

	type candidate struct {
		uuid     string
		firstMsg string
		started  time.Time
		modTime  time.Time
	}

	candidates := make([]candidate, 0, len(files))
	for _, f := range files {
		meta := readSessionMeta(filepath.Join(projectDir, f.uuid+".jsonl"))
		candidates = append(candidates, candidate{
			uuid:     f.uuid,
			firstMsg: meta.FirstMessage,
			started:  meta.Started,
			modTime:  f.modTime,
		})
	}

	if len(candidates) == 0 {
		return "", ""
	}

	// Strategy 1: Content matching — check if recent messages from each
	// session file appear in the tmux pane output. Most reliable because
	// it directly verifies which conversation is on screen.
	if paneContent != "" && len(candidates) > 1 {
		var bestMatch *candidate
		bestScore := 0
		for i := range candidates {
			c := &candidates[i]
			path := filepath.Join(projectDir, c.uuid+".jsonl")
			snippets := readLastUserMessages(path, 3)
			score := 0
			for _, s := range snippets {
				if strings.Contains(paneContent, s) {
					score++
				}
			}
			if score > bestScore {
				bestScore = score
				bestMatch = c
			}
		}
		if bestMatch != nil && bestScore > 0 {
			return bestMatch.uuid, bestMatch.firstMsg
		}
	}

	// Strategy 2: Find session whose first message is closest to (and after)
	// sessionStart. No fixed window — the closest start time is always best.
	if !sessionStart.IsZero() {
		var bestMatch *candidate
		var bestDiff time.Duration = -1
		for i := range candidates {
			c := &candidates[i]
			if c.started.IsZero() {
				continue
			}
			diff := c.started.Sub(sessionStart)
			if diff >= 0 && (bestDiff < 0 || diff < bestDiff) {
				bestDiff = diff
				bestMatch = c
			}
		}
		if bestMatch != nil {
			return bestMatch.uuid, bestMatch.firstMsg
		}
	}

	// Strategy 3: File modified during this session's lifetime (handles
	// resumed sessions where started predates this tmux session).
	if !sessionStart.IsZero() {
		var bestMatch *candidate
		for i := range candidates {
			c := &candidates[i]
			if c.modTime.Before(sessionStart) {
				continue
			}
			if bestMatch == nil || c.modTime.After(bestMatch.modTime) {
				bestMatch = c
			}
		}
		if bestMatch != nil {
			return bestMatch.uuid, bestMatch.firstMsg
		}
	}

	// Strategy 4: Last resort — most recently modified file.
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

// readLastUserMessages reads the last n user messages from a JSONL session
// file and returns normalized text snippets suitable for substring matching.
func readLastUserMessages(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	var msgs []string
	for scanner.Scan() {
		var msg struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type != "user" {
			continue
		}
		content := extractContent(msg.Message.Content)
		if content == "" || strings.HasPrefix(content, "<command-message>") {
			continue
		}
		// Normalize: collapse whitespace, take a meaningful snippet
		content = strings.Join(strings.Fields(content), " ")
		if len(content) > 80 {
			content = content[:80]
		}
		msgs = append(msgs, content)
	}

	// Return last n
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs
}

// ReadSessionPreview reads a JSONL session file and returns a formatted
// conversation preview showing the last maxMessages user/assistant messages.
func ReadSessionPreview(workDir, uuid string, maxMessages int) string {
	if uuid == "" {
		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	encoded := encodeProjectDir(workDir)
	path := filepath.Join(home, ".claude", "projects", encoded, uuid+".jsonl")

	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	type chatLine struct {
		role    string // "You" or "Claude"
		content string
	}
	var lines []chatLine

	for scanner.Scan() {
		var msg struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		var role string
		switch msg.Type {
		case "user":
			role = "You"
		case "assistant":
			role = "Claude"
		default:
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

		// Collapse whitespace
		content = strings.Join(strings.Fields(content), " ")

		// Truncate long messages
		if len(content) > 200 {
			content = content[:197] + "..."
		}

		lines = append(lines, chatLine{role: role, content: content})
	}

	// Keep last maxMessages
	if len(lines) > maxMessages {
		lines = lines[len(lines)-maxMessages:]
	}

	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(l.role)
		b.WriteString(": ")
		b.WriteString(l.content)
	}
	return b.String()
}

// encodeProjectDir encodes a directory path the same way Claude Code does
// for its project session storage: slashes become hyphens, leading slash
// is included (so /Users/foo becomes -Users-foo).
func encodeProjectDir(dir string) string {
	return strings.ReplaceAll(dir, "/", "-")
}

// SessionFileModTime returns the modification time of a specific session file.
// Much cheaper than scanning the entire directory — just one stat call.
func SessionFileModTime(workDir, uuid string) time.Time {
	if workDir == "" || uuid == "" {
		return time.Time{}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return time.Time{}
	}
	path := filepath.Join(home, ".claude", "projects", encodeProjectDir(workDir), uuid+".jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

