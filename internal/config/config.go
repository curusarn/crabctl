package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var bayRe = regexp.MustCompile(`bay[^0-9]*(\d+)`)

type HostConfig struct {
	Host   string `yaml:"host"`
	User   string `yaml:"user"`
	SSHKey string `yaml:"ssh_key"`
	Prefix string `yaml:"prefix"`
}

type Config struct {
	Hosts map[string]HostConfig `yaml:"hosts"`
}

// Load reads the config from ~/.config/crabctl/config.yaml.
// Returns an empty config if the file doesn't exist.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return &Config{}, nil
	}

	var cfg Config

	path := filepath.Join(home, ".config", "crabctl", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}

	// Default prefix to "crab-" if not set
	for name, h := range cfg.Hosts {
		if h.Prefix == "" {
			h.Prefix = "crab-"
		} else if !strings.HasSuffix(h.Prefix, "-") {
			h.Prefix += "-"
		}
		// Expand ~ in ssh_key
		if len(h.SSHKey) > 0 && h.SSHKey[0] == '~' {
			h.SSHKey = filepath.Join(home, h.SSHKey[1:])
		}
		cfg.Hosts[name] = h
	}

	// Fall back to env vars for workbench auto-discovery
	if len(cfg.Hosts) == 0 {
		if wh := os.Getenv("WORKBENCH_HOST"); wh != "" {
			prefix := os.Getenv("WORKBENCH_USER")
			if prefix == "" {
				prefix = os.Getenv("USER")
			}
			if prefix != "" {
				prefix += "-"
			}
			if cfg.Hosts == nil {
				cfg.Hosts = make(map[string]HostConfig)
			}
			nickname := "workbench"
			if m := bayRe.FindStringSubmatch(wh); m != nil {
				nickname = "bay" + m[1]
			}
			cfg.Hosts[nickname] = HostConfig{
				Host:   wh,
				User:   "root",
				Prefix: prefix,
			}
		}
	}

	return &cfg, nil
}
