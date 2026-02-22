package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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
		// No server running is not an error
		return nil, nil
	}
	return parseSessionList(out, s.Prefix), nil
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

func (s *SSHExecutor) NewSession(name, workDir string, claudeArgs []string) error {
	fullName := s.Prefix + name
	cmd := fmt.Sprintf("tmux new-session -d -s %s", shellQuote(fullName))
	if workDir != "" {
		cmd += fmt.Sprintf(" -c %s", shellQuote(workDir))
	}

	_, err := s.run(cmd)
	if err != nil {
		return err
	}

	// Send claude command via send-keys to avoid quoting issues through SSH
	claudeCmd := "unset CLAUDECODE; claude"
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

	return nil
}

func (s *SSHExecutor) SendKeys(fullName, text string) error {
	_, err := s.run(fmt.Sprintf("tmux send-keys -t %s -l %s && tmux send-keys -t %s Enter",
		shellQuote(fullName), shellQuote(text), shellQuote(fullName)))
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

func (s *SSHExecutor) GetBranchPR(workDir string) (string, string) {
	// Source .envrc from workDir for credentials (GH_TOKEN), then run gh in the target dir
	envSetup := fmt.Sprintf("cd %s && [ -f .envrc ] && . .envrc >/dev/null 2>&1;", shellQuote(workDir))
	ghCmd := "gh pr view --json number,url --jq '\"PR #\\(.number) \\(.url)\"'"

	out, err := s.run(fmt.Sprintf("%s cd %s && %s", envSetup, shellQuote(workDir), ghCmd))
	if err == nil {
		if pr, prURL := ParsePROutput(strings.TrimSpace(out)); pr != "" {
			return pr, prURL
		}
	}
	// If workDir is not a git repo or has no PR, scan subdirs
	for _, sub := range s.findGitSubdirs(workDir) {
		out, err := s.run(fmt.Sprintf("%s cd %s && %s", envSetup, shellQuote(sub), ghCmd))
		if err == nil {
			if pr, prURL := ParsePROutput(strings.TrimSpace(out)); pr != "" {
				return pr, prURL
			}
		}
	}
	return "", ""
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
