package cmd

import (
	"strings"

	"github.com/simon/crabctl/internal/config"
	"github.com/simon/crabctl/internal/tmux"
)

// parseHostName splits "host:name" into (host, name).
// If no colon, returns ("", name).
func parseHostName(s string) (host, name string) {
	if idx := strings.IndexByte(s, ':'); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return "", s
}

// resolveExecutor returns an executor for the given host nickname.
// Empty host returns a LocalExecutor.
func resolveExecutor(host string) tmux.Executor {
	if host == "" {
		return &tmux.LocalExecutor{}
	}

	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return &tmux.LocalExecutor{}
	}

	h, ok := cfg.Hosts[host]
	if !ok {
		return &tmux.LocalExecutor{}
	}

	return &tmux.SSHExecutor{
		Nickname: host,
		Host:     h.Host,
		User:     h.User,
		SSHKey:   h.SSHKey,
		Prefix:   h.Prefix,
	}
}
