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

// sessionMeta holds the cwd and first message extracted from a JSONL file.
type sessionMeta struct {
	CWD          string
	FirstMessage string
}

// readSessionMeta reads the cwd and first user message from a JSONL session file.
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
			Type    string `json:"type"`
			CWD     string `json:"cwd"`
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

		if meta.CWD == "" && msg.CWD != "" {
			meta.CWD = msg.CWD
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

		// Got both, stop reading
		if meta.CWD != "" {
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
