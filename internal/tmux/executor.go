package tmux

// Executor abstracts tmux operations so they can run locally or over SSH.
type Executor interface {
	HostName() string
	SessionPrefix() string
	ListSessions() ([]SessionInfo, error)
	CapturePaneOutput(fullName string, lines int) (string, error)
	NewSession(name, workDir string, claudeArgs []string) error
	SendKeys(fullName, text string) error
	KillSession(fullName string) error
	HasSession(fullName string) bool
	GetPanePath(fullName string) string
	AttachSession(fullName string) error
}
