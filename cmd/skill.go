package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const skillURL = "https://raw.githubusercontent.com/curusarn/crabctl/main/.claude/skills/crabs/SKILL.md"

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Install the crabs skill into the current project",
	Long: `Downloads the crabs skill from GitHub and installs it into
the current project for both Claude Code and OpenCode:

  .claude/skills/crabs/SKILL.md
  .opencode/skills/crabs/SKILL.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := http.Get(skillURL)
		if err != nil {
			return fmt.Errorf("failed to fetch skill: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to fetch skill: HTTP %d", resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read skill: %w", err)
		}

		targets := []string{
			filepath.Join(".claude", "skills", "crabs", "SKILL.md"),
			filepath.Join(".opencode", "skills", "crabs", "SKILL.md"),
		}

		for _, target := range targets {
			dir := filepath.Dir(target)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("failed to create %s: %w", dir, err)
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return fmt.Errorf("failed to write %s: %w", target, err)
			}
			fmt.Printf("Installed %s\n", target)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(skillCmd)
}
