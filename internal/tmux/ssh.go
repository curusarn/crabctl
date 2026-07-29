package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SSHExecutor runs tmux commands on a remote host over SSH.
type SSHExecutor struct {
	Nickname string
	Host     string
	User     string
	SSHKey   string
	Prefix   string
}

func (s *SSHExecutor) HostName() string      { return s.Nickname }
func (s *SSHExecutor) SessionPrefix() string { return s.Prefix }

func (s *SSHExecutor) sshArgs() []string {
	args := []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=/tmp/crabctl-ssh-%r@%h:%p",
		"-o", "ControlPersist=60",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if s.SSHKey != "" {
		args = append(args, "-i", s.SSHKey)
	}
	args = append(args, fmt.Sprintf("%s@%s", s.User, s.Host))
	return args
}

func (s *SSHExecutor) run(remoteCmd string) (string, error) {
	args := append(s.sshArgs(), remoteCmd)
	cmd := exec.Command("ssh", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *SSHExecutor) ListSessions() ([]SessionInfo, error) {
	out, err := s.run(fmt.Sprintf("tmux list-sessions -F '#{session_name}|#{session_attached}|#{session_created}' 2>/dev/null"))
	if err != nil {
		// SSH exit code 255 = connection failure; other codes mean the
		// remote command ran but failed (e.g., tmux not running).
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() != 255 {
			return nil, nil // tmux not running, not an SSH error
		}
		return nil, err // SSH connection failure
	}
	sessions := parseSessionList(out, s.Prefix)
	if len(sessions) > 0 {
		s.fetchParents(sessions)
	}
	return sessions, nil
}

// fetchParents batch-fetches CRABCTL_PARENT for all sessions in a single SSH call.
func (s *SSHExecutor) fetchParents(sessions []SessionInfo) {
	// Build a shell loop that prints "fullName|CRABCTL_PARENT=value" for each session
	var names []string
	for _, sess := range sessions {
		names = append(names, shellQuote(sess.FullName))
	}
	cmd := fmt.Sprintf("for s in %s; do printf '%%s|%%s\\n' \"$s\" \"$(tmux show-env -t \"$s\" CRABCTL_PARENT 2>/dev/null)\"; done",
		strings.Join(names, " "))
	out, err := s.run(cmd)
	if err != nil {
		return
	}
	// Parse output: "crab-foo|CRABCTL_PARENT=crab-bar"
	parentMap := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		// parts[1] is "CRABCTL_PARENT=value"
		if idx := strings.Index(parts[1], "="); idx >= 0 {
			parentMap[parts[0]] = parts[1][idx+1:]
		}
	}
	for i := range sessions {
		if p, ok := parentMap[sessions[i].FullName]; ok {
			sessions[i].Parent = p
		}
	}
}

func (s *SSHExecutor) CapturePaneOutput(fullName string, lines int) (string, error) {
	out, err := s.run(fmt.Sprintf("tmux capture-pane -t %s -p -e -S -%d", shellQuote(fullName), lines))
	if err != nil {
		return "", err
	}
	cleaned := stripDimText(out)
	cleaned = ansiRe.ReplaceAllString(cleaned, "")
	return cleaned, nil
}

func (s *SSHExecutor) NewSession(name, workDir string, claudeArgs []string, parent string) error {
	fullName := s.Prefix + name
	cmd := fmt.Sprintf("tmux new-session -d -s %s -e %s",
		shellQuote(fullName), shellQuote("CRABCTL_NAME="+fullName))
	if workDir != "" {
		cmd += fmt.Sprintf(" -c %s", shellQuote(workDir))
	}

	_, err := s.run(cmd)
	if err != nil {
		return err
	}

	// Send claude command via send-keys to avoid quoting issues through SSH.
	// Per-session CDP_PROFILE: same isolation as the local path (see tmux.go).
	claudeCmd := cdpProfileExport(fullName) + "unset CLAUDECODE; claude"
	for _, a := range claudeArgs {
		claudeCmd += " " + a
	}
	s.run(fmt.Sprintf("tmux send-keys -t %s -l %s", shellQuote(fullName), shellQuote(claudeCmd)))
	s.run(fmt.Sprintf("tmux send-keys -t %s Enter", shellQuote(fullName)))

	// Store claude flags
	if len(claudeArgs) > 0 {
		s.run(fmt.Sprintf("tmux set-environment -t %s CRABCTL_FLAGS %s",
			shellQuote(fullName), shellQuote(strings.Join(claudeArgs, " "))))
	}

	// Store parent reference
	if parent != "" {
		s.run(fmt.Sprintf("tmux set-environment -t %s CRABCTL_PARENT %s",
			shellQuote(fullName), shellQuote(parent)))
	}

	return nil
}

func (s *SSHExecutor) SendKeys(fullName, text string) error {
	if _, err := s.run(fmt.Sprintf("tmux send-keys -t %s -l %s",
		shellQuote(fullName), shellQuote(text))); err != nil {
		return err
	}
	// Pause to let Claude Code finalize its "[Pasted text #N]" markers
	// before submitting — see the local SendKeys comment for context.
	time.Sleep(PostPasteSettleDelay)
	_, err := s.run(fmt.Sprintf("tmux send-keys -t %s Enter", shellQuote(fullName)))
	return err
}

func (s *SSHExecutor) KillSession(fullName string) error {
	s.run(fmt.Sprintf("tmux send-keys -t %s C-c ''", shellQuote(fullName)))
	_, err := s.run(fmt.Sprintf("sleep 0.5 && tmux kill-session -t %s", shellQuote(fullName)))
	return err
}

func (s *SSHExecutor) HasSession(fullName string) bool {
	_, err := s.run(fmt.Sprintf("tmux has-session -t %s 2>/dev/null", shellQuote(fullName)))
	return err == nil
}

func (s *SSHExecutor) GetPanePath(fullName string) string {
	out, err := s.run(fmt.Sprintf("tmux display-message -t %s -p '#{pane_current_path}'", shellQuote(fullName)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (s *SSHExecutor) GetBranchPR(workDir string) (string, string, string) {
	// Source .envrc from workDir for credentials (GH_TOKEN), then run gh in the target dir
	envSetup := fmt.Sprintf("cd %s && [ -f .envrc ] && . .envrc >/dev/null 2>&1;", shellQuote(workDir))
	ghCmd := "gh pr view --json number,url,state,isDraft --jq '\"PR #\\(.number) \\(.url) \\(.state) \\(.isDraft)\"'"

	out, err := s.run(fmt.Sprintf("%s cd %s && %s", envSetup, shellQuote(workDir), ghCmd))
	if err == nil {
		if pr, prURL, prState := ParsePROutput(strings.TrimSpace(out)); pr != "" {
			return pr, prURL, prState
		}
	}
	// If workDir is not a git repo or has no PR, scan subdirs
	for _, sub := range s.findGitSubdirs(workDir) {
		out, err := s.run(fmt.Sprintf("%s cd %s && %s", envSetup, shellQuote(sub), ghCmd))
		if err == nil {
			if pr, prURL, prState := ParsePROutput(strings.TrimSpace(out)); pr != "" {
				return pr, prURL, prState
			}
		}
	}
	return "", "", ""
}

// findGitSubdirs returns immediate subdirectories containing .git via a single SSH call.
// Returns at most 10 results to avoid excessive scanning.
func (s *SSHExecutor) findGitSubdirs(dir string) []string {
	out, err := s.run(fmt.Sprintf("for d in %s/*/; do [ -e \"$d/.git\" ] && echo \"$d\"; done", shellQuote(dir)))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			dirs = append(dirs, strings.TrimSuffix(line, "/"))
			if len(dirs) >= 10 {
				break
			}
		}
	}
	return dirs
}

func (s *SSHExecutor) ReadHistoryTail(n int) (string, error) {
	out, err := s.run(fmt.Sprintf("tail -n %d ~/.claude/history.jsonl 2>/dev/null", n))
	if err != nil {
		return "", err
	}
	return out, nil
}

func (s *SSHExecutor) AttachSession(fullName string) error {
	args := []string{"-t"}
	args = append(args, s.sshArgs()...)
	args = append(args, fmt.Sprintf("tmux attach-session -t %s", shellQuote(fullName)))
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = filterTMUX(os.Environ())
	return cmd.Run()
}

// shellQuote wraps a string in single quotes, escaping any single quotes inside.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
