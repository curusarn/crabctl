package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

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

	path := filepath.Join(home, ".config", "crabctl", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Default prefix to "crab-" if not set
	for name, h := range cfg.Hosts {
		if h.Prefix == "" {
			h.Prefix = "crab-"
		}
		// Expand ~ in ssh_key
		if len(h.SSHKey) > 0 && h.SSHKey[0] == '~' {
			h.SSHKey = filepath.Join(home, h.SSHKey[1:])
		}
		cfg.Hosts[name] = h
	}

	return &cfg, nil
}
