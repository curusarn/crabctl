package session

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
