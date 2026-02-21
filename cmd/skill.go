package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var embeddedSkillContent []byte

func SetSkillContent(content []byte) {
	embeddedSkillContent = content
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Install the crab skill for Claude Code",
	Long:  `Installs the crab skill globally to ~/.claude/skills/crab/SKILL.md.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(embeddedSkillContent) == 0 {
			return fmt.Errorf("skill content not embedded (build with make)")
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home dir: %w", err)
		}

		skillDir := filepath.Join(home, ".claude", "skills", "crab")
		skillPath := filepath.Join(skillDir, "SKILL.md")

		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return fmt.Errorf("failed to create %s: %w", skillDir, err)
		}

		if err := os.WriteFile(skillPath, embeddedSkillContent, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", skillPath, err)
		}
		fmt.Printf("Installed %s\n", skillPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(skillCmd)
}
