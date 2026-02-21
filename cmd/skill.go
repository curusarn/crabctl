package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var embeddedSkillContent []byte

func SetSkillContent(content []byte) {
	embeddedSkillContent = content
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Install the crab skill for Claude Code",
	Long: `Installs the crab skill so Claude Code can manage crab sessions.

By default, prompts to choose between:
  - User-wide:    ~/.claude/skills/crab/SKILL.md
  - Project-local: .claude/skills/crab/SKILL.md

Use -g/--global or -l/--local to skip the prompt.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		global, _ := cmd.Flags().GetBool("global")
		local, _ := cmd.Flags().GetBool("local")

		if global && local {
			return fmt.Errorf("cannot use both --global and --local")
		}

		if len(embeddedSkillContent) == 0 {
			return fmt.Errorf("skill content not embedded (build with make)")
		}

		var userWide bool
		switch {
		case global:
			userWide = true
		case local:
			userWide = false
		default:
			choice, err := promptInstallLocation()
			if err != nil {
				return err
			}
			userWide = choice
		}

		return installSkill(userWide)
	},
}

func promptInstallLocation() (userWide bool, err error) {
	fmt.Println("Where to install the crab skill?")
	fmt.Println()
	fmt.Println("  1. User-wide  (~/.claude/skills/)")
	fmt.Println("  2. Project-local (.claude/skills/)")
	fmt.Println()
	fmt.Print("Choose [1]: ")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	switch input {
	case "", "1":
		return true, nil
	case "2":
		return false, nil
	default:
		return false, fmt.Errorf("invalid choice: %q (expected 1 or 2)", input)
	}
}

func installSkill(userWide bool) error {
	var baseDir string
	var hint string

	if userWide {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home dir: %w", err)
		}
		baseDir = filepath.Join(home, ".claude")
		hint = "The skill is now available in all your Claude Code sessions."
	} else {
		baseDir = ".claude"
		hint = "The skill is available in this project. Consider adding .claude/skills/ to .gitignore."
	}

	targets := []string{
		filepath.Join(baseDir, "skills", "crab", "SKILL.md"),
	}

	// Also install for OpenCode in the same scope
	if userWide {
		home, _ := os.UserHomeDir()
		targets = append(targets, filepath.Join(home, ".opencode", "skills", "crab", "SKILL.md"))
	} else {
		targets = append(targets, filepath.Join(".opencode", "skills", "crab", "SKILL.md"))
	}

	for _, target := range targets {
		dir := filepath.Dir(target)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
		if err := os.WriteFile(target, embeddedSkillContent, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", target, err)
		}
		fmt.Printf("Installed %s\n", target)
	}

	fmt.Println()
	fmt.Println(hint)
	return nil
}

func init() {
	skillCmd.Flags().BoolP("global", "g", false, "Install user-wide to ~/.claude/skills/ (skip prompt)")
	skillCmd.Flags().BoolP("local", "l", false, "Install project-local to .claude/skills/ (skip prompt)")
	rootCmd.AddCommand(skillCmd)
}
