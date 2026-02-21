package main

import (
	_ "embed"

	"github.com/simon/crabctl/cmd"
)

//go:embed .claude/skills/crab/SKILL.md
var skillContent []byte

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit)
	cmd.SetSkillContent(skillContent)
	cmd.Execute()
}
