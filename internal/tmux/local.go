package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LocalExecutor runs tmux commands on the local machine.
type LocalExecutor struct{}

func (l *LocalExecutor) HostName() string      { return "" }
func (l *LocalExecutor) SessionPrefix() string { return SessionPrefix }

func (l *LocalExecutor) ListSessions() ([]SessionInfo, error) {
	return listSessionsWithPrefix(SessionPrefix)
}

func (l *LocalExecutor) CapturePaneOutput(fullName string, lines int) (string, error) {
	return CapturePaneOutput(fullName, lines)
}

func (l *LocalExecutor) NewSession(name, workDir string, claudeArgs []string, parent string) error {
	return NewSession(name, workDir, claudeArgs, parent)
}

func (l *LocalExecutor) SendKeys(fullName, text string) error {
	return SendKeys(fullName, text)
}

func (l *LocalExecutor) KillSession(fullName string) error {
	return KillSession(fullName)
}

func (l *LocalExecutor) HasSession(fullName string) bool {
	return HasSession(fullName)
}

func (l *LocalExecutor) GetPanePath(fullName string) string {
	return GetPanePath(fullName)
}

func (l *LocalExecutor) GetBranchPR(workDir string) (string, string, string) {
	pr, prURL, prState := getBranchPRLocal(workDir)
	if pr != "" {
		return pr, prURL, prState
	}
	// If workDir is not a git repo, scan subdirs
	for _, sub := range findGitSubdirs(workDir) {
		pr, prURL, prState = getBranchPRLocal(sub)
		if pr != "" {
			return pr, prURL, prState
		}
	}
	return "", "", ""
}

func getBranchPRLocal(dir string) (string, string, string) {
	cmd := exec.Command("gh", "pr", "view", "--json", "number,url,state,isDraft", "--jq", `"PR #\(.number) \(.url) \(.state) \(.isDraft)"`)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", "", ""
	}
	return ParsePROutput(strings.TrimSpace(string(out)))
}

const maxGitSubdirs = 10

// findGitSubdirs returns immediate subdirectories of dir that contain a .git directory.
// Returns at most maxGitSubdirs results to avoid excessive scanning.
func findGitSubdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(sub, ".git")); err == nil {
			dirs = append(dirs, sub)
			if len(dirs) >= maxGitSubdirs {
				break
			}
		}
	}
	return dirs
}

func (l *LocalExecutor) AttachSession(fullName string) error {
	return RunAttachSession(fullName)
}

func (l *LocalExecutor) ReadHistoryTail(n int) (string, error) {
	return ReadHistoryTail(n)
}

// ParsePROutput parses "PR #123 https://github.com/owner/repo/pull/123 OPEN false" into (pr, prURL, prState).
// Also handles the legacy format without state/isDraft fields.
func ParsePROutput(line string) (string, string, string) {
	if !strings.HasPrefix(line, "PR #") {
		return "", "", ""
	}
	parts := strings.SplitN(line, " ", 5)
	if len(parts) < 3 {
		return "", "", ""
	}
	pr := parts[0] + " " + parts[1]
	prURL := parts[2]
	prState := "open" // default
	if len(parts) >= 5 {
		state := parts[3]  // "OPEN", "MERGED", "CLOSED"
		isDraft := parts[4] // "true", "false"
		switch {
		case state == "OPEN" && isDraft == "true":
			prState = "draft"
		case state == "MERGED":
			prState = "merged"
		case state == "CLOSED":
			prState = "closed"
		default:
			prState = "open"
		}
	}
	return pr, prURL, prState
}

// listSessionsWithPrefix lists tmux sessions with the given prefix.
func listSessionsWithPrefix(prefix string) ([]SessionInfo, error) {
	tmuxBin, err := FindTmux()
	if err != nil {
		return nil, fmt.Errorf("tmux not found: %w", err)
	}

	out, err := runCommand(tmuxBin, "list-sessions", "-F", "#{session_name}|#{session_attached}|#{session_created}")
	if err != nil {
		return nil, nil
	}

	sessions := parseSessionList(out, prefix)
	for i := range sessions {
		if p := GetSessionEnv(sessions[i].FullName, "CRABCTL_PARENT"); p != "" {
			sessions[i].Parent = p
		}
	}
	return sessions, nil
}

// parseSessionList parses tmux list-sessions output into SessionInfo structs.
func parseSessionList(output, prefix string) []SessionInfo {
	var sessions []SessionInfo
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		fullName := parts[0]
		if !strings.HasPrefix(fullName, prefix) {
			continue
		}

		attached, _ := strconv.Atoi(parts[1])
		createdUnix, _ := strconv.ParseInt(parts[2], 10, 64)

		sessions = append(sessions, SessionInfo{
			Name:          strings.TrimPrefix(fullName, prefix),
			FullName:      fullName,
			AttachedCount: attached,
			Created:       time.Unix(createdUnix, 0),
		})
	}
	return sessions
}
