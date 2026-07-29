package tmux

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/simon/crabctl/internal/state"
)

// DetectParent determines the parent session key for a new session.
// Priority:
//  1. explicit flag
//  2. $TMUX_PANE-targeted tmux query (exact; only directly-spawned pane
//     shells carry TMUX_PANE)
//  3. CLAUDE_CODE_SESSION_ID mapped through the state DB (injected fresh
//     per command by Claude Code, so it names the true caller even in
//     daemon-routed shells)
//  4. process ancestry: nearest ancestor that is a tmux pane root
//  5. CRABCTL_NAME env var, unless running under a Claude Code bg-pty-host
//
// Why the paranoia: Claude Code routes some Bash commands through a shared
// per-user daemon ("bg-pty-host" pool). Those shells inherit the env of
// whichever session first spawned the pty-host, so CRABCTL_NAME (and TMUX
// vars) in here may belong to a DIFFERENT, possibly dead, crab. That is how
// children got recorded as siblings under the wrong parent.
func DetectParent(explicit string) string {
	if explicit != "" {
		return explicit
	}

	// Exact: ask tmux about our own pane. Never query untargeted: a
	// tty-less client makes the server guess the pane, and a failed guess
	// silently returns the most recently active session instead.
	if os.Getenv("TMUX") != "" {
		if pane := os.Getenv("TMUX_PANE"); pane != "" {
			if name := sessionForPane(pane); strings.HasPrefix(name, SessionPrefix) {
				return name
			}
		}
	}

	// No usable TMUX_PANE (daemon-routed shells strip TMUX vars). The
	// harness still injects the CALLER's Claude session id per command,
	// so it survives pty-host reuse; map it via the state DB, which the
	// TUI keeps in sync with each crab's Claude session file.
	if sid := os.Getenv("CLAUDE_CODE_SESSION_ID"); sid != "" {
		if store, err := state.Open(); err == nil {
			name := store.SessionByFile(sid)
			store.Close()
			if strings.HasPrefix(name, SessionPrefix) {
				return name
			}
		}
	}

	// Walk our ancestors. Hitting a tmux pane root identifies the session
	// we (or the daemon running us) live in.
	ancestors, sawPtyHost := ancestry()
	if name := sessionForAncestors(ancestors); strings.HasPrefix(name, SessionPrefix) {
		return name
	}

	// CRABCTL_NAME is trustworthy only outside the pty-host pool; in a
	// pty-host it may be another session's identity. Better no parent
	// than a wrong one.
	if !sawPtyHost {
		if name := os.Getenv("CRABCTL_NAME"); name != "" {
			return name
		}
	}

	return ""
}

// sessionForPane resolves a pane id to its session name via tmux.
func sessionForPane(pane string) string {
	tmuxBin, err := FindTmux()
	if err != nil {
		return ""
	}
	out, err := exec.Command(tmuxBin, "display-message", "-p", "-t", pane, "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ancestry returns our ancestor pids (nearest first) and whether any
// ancestor is a Claude Code daemon / bg-pty-host process.
func ancestry() ([]int, bool) {
	var pids []int
	sawPtyHost := false
	pid := os.Getppid()
	for i := 0; i < 20 && pid > 1; i++ {
		pids = append(pids, pid)
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=,command=").Output()
		if err != nil {
			break
		}
		fields := strings.Fields(string(out))
		if len(fields) == 0 {
			break
		}
		ppid, err := strconv.Atoi(fields[0])
		if err != nil {
			break
		}
		cmd := strings.Join(fields[1:], " ")
		if strings.Contains(cmd, "bg-pty-host") || strings.Contains(cmd, "daemon run") {
			sawPtyHost = true
		}
		pid = ppid
	}
	return pids, sawPtyHost
}

// sessionForAncestors maps ancestor pids against tmux pane root pids and
// returns the session of the nearest match.
func sessionForAncestors(pids []int) string {
	tmuxBin, err := FindTmux()
	if err != nil {
		return ""
	}
	out, err := exec.Command(tmuxBin, "list-panes", "-a", "-F", "#{pane_pid} #{session_name}").Output()
	if err != nil {
		return ""
	}
	paneSessions := make(map[int]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		if p, err := strconv.Atoi(parts[0]); err == nil {
			paneSessions[p] = parts[1]
		}
	}
	for _, pid := range pids {
		if s, ok := paneSessions[pid]; ok {
			return s
		}
	}
	return ""
}
