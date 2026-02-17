package main

import "github.com/simon/crabctl/cmd"

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	cmd.SetVersionInfo(version, commit)
	cmd.Execute()
}
