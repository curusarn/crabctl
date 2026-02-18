package tui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/simon/crabctl/internal/session"
	"github.com/simon/crabctl/internal/tmux"
)

const pollInterval = 1500 * time.Millisecond

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type tickMsg time.Time

type sessionCreatedMsg struct {
	Name string
	Err  error
}

// remoteSessionsMsg carries sessions from a single remote host.
// These get merged into the existing session list.
type remoteSessionsMsg struct {
	Host     string
	Sessions []session.Session
}

type previewOutputMsg struct {
	Output string
}

type previewState struct {
	SessionName string
	FullName    string
	Host        string
	Output      string
}

type confirmAction struct {
	SessionName string
	FullName    string
	Host        string
}

type Model struct {
	sessions      []session.Session
	filtered      []session.Session
	cursor        int
	scrollOffset  int
	input         textinput.Model
	preview       *previewState
	confirmKill   *confirmAction
	executors     []tmux.Executor
	width, height int
	AttachTarget  string // set when user confirms attach
	AttachHost    string // host of session to attach
	quitting      bool
	err           error
}

func NewModel(executors []tmux.Executor) Model {
	ti := textinput.New()
	ti.Placeholder = "Type to filter or enter command..."
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 60

	return Model{
		input:     ti,
		executors: executors,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
		m.refreshLocalSessions,
		tickCmd(),
	}
	cmds = append(cmds, m.refreshRemoteSessions()...)
	return tea.Batch(cmds...)
}

// refreshLocalSessions fetches only local sessions (fast).
func (m Model) refreshLocalSessions() tea.Msg {
	for _, ex := range m.executors {
		if ex.HostName() == "" {
			sessions, err := session.ListExecutor(ex)
			if err != nil {
				return err
			}
			return sessions
		}
	}
	return []session.Session(nil)
}

// refreshRemoteSessions returns commands that fetch each remote host in parallel.
func (m Model) refreshRemoteSessions() []tea.Cmd {
	var cmds []tea.Cmd
	for _, ex := range m.executors {
		if ex.HostName() != "" {
			ex := ex // capture
			cmds = append(cmds, func() tea.Msg {
				sessions, _ := session.ListExecutor(ex)
				return remoteSessionsMsg{
					Host:     ex.HostName(),
					Sessions: sessions,
				}
			})
		}
	}
	return cmds
}

func (m Model) capturePreviewCmd(fullName, host string) tea.Cmd {
	exec := m.findExecutor(host)
	return func() tea.Msg {
		output, err := exec.CapturePaneOutput(fullName, 50)
		if err != nil {
			return previewOutputMsg{Output: "Error: " + err.Error()}
		}
		return previewOutputMsg{Output: cleanPreviewOutput(output)}
	}
}

func (m Model) findExecutor(host string) tmux.Executor {
	for _, e := range m.executors {
		if e.HostName() == host {
			return e
		}
	}
	return &tmux.LocalExecutor{}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case sessionCreatedMsg:
		if msg.Err != nil {
			m.err = msg.Err
		}
		m.input.SetValue("")
		return m, m.refreshLocalSessions

	case []session.Session:
		// Local sessions replace only local entries, preserve remote
		remote := filterByHost(m.sessions, true)
		m.sessions = append(msg, remote...)
		session.SortSessions(m.sessions)
		if m.preview == nil {
			m.applyFilter()
		}
		return m, nil

	case remoteSessionsMsg:
		// Replace sessions for this specific host, keep everything else
		var kept []session.Session
		for _, s := range m.sessions {
			if s.Host != msg.Host {
				kept = append(kept, s)
			}
		}
		m.sessions = append(kept, msg.Sessions...)
		session.SortSessions(m.sessions)
		if m.preview == nil {
			m.applyFilter()
		}
		return m, nil

	case error:
		m.err = msg
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{tickCmd(), m.refreshLocalSessions}
		cmds = append(cmds, m.refreshRemoteSessions()...)
		if m.preview != nil {
			cmds = append(cmds, m.capturePreviewCmd(m.preview.FullName, m.preview.Host))
		}
		return m, tea.Batch(cmds...)

	case previewOutputMsg:
		if m.preview != nil {
			m.preview.Output = msg.Output
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = msg.Width - 4
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C always quits
	if key.Matches(msg, keys.CtrlC) {
		m.quitting = true
		return m, tea.Quit
	}

	// Escape
	if key.Matches(msg, keys.Escape) {
		if m.confirmKill != nil {
			m.confirmKill = nil
			return m, nil
		}
		if m.preview != nil {
			m.preview = nil
			m.input.SetValue("")
			m.applyFilter()
			return m, nil
		}
		m.input.SetValue("")
		m.applyFilter()
		return m, nil
	}

	// If kill confirmation is pending, only Enter proceeds
	if m.confirmKill != nil {
		if key.Matches(msg, keys.Enter) {
			return m.executeKill()
		}
		// Any other key cancels
		m.confirmKill = nil
		return m, nil
	}

	// Ctrl+K: kill selected session
	if key.Matches(msg, keys.Kill) {
		if sel := m.selectedSession(); sel != nil {
			m.confirmKill = &confirmAction{
				SessionName: sel.Name,
				FullName:    sel.FullName,
				Host:        sel.Host,
			}
		}
		return m, nil
	}

	// q quits only when input is empty and no preview
	if key.Matches(msg, keys.Quit) && m.input.Value() == "" && m.preview == nil {
		m.quitting = true
		return m, tea.Quit
	}

	// Preview mode key handling
	if m.preview != nil {
		return m.handlePreviewKey(msg)
	}

	// Normal mode (no preview)
	return m.handleNormalKey(msg)
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Navigation: only when input is empty
	if m.input.Value() == "" {
		if key.Matches(msg, keys.Up) {
			if m.cursor > 0 {
				m.cursor--
				m.ensureCursorVisible()
			}
			return m, nil
		}
		if key.Matches(msg, keys.Down) {
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.ensureCursorVisible()
			}
			return m, nil
		}
	}

	// Enter
	if key.Matches(msg, keys.Enter) {
		text := strings.TrimSpace(m.input.Value())

		// /new command: create a new session
		if cmd := parseNewCommand(text); cmd != nil {
			m.input.SetValue("")
			return m, cmd
		}

		// Open preview
		sel := m.selectedSession()
		if sel == nil {
			return m, nil
		}
		m.preview = &previewState{
			SessionName: sel.Name,
			FullName:    sel.FullName,
			Host:        sel.Host,
		}
		m.input.SetValue("")
		return m, m.capturePreviewCmd(sel.FullName, sel.Host)
	}

	// Default: update text input and refilter
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.applyFilter()
	return m, cmd
}

func (m Model) handlePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Navigation: switch between sessions while previewing
	if m.input.Value() == "" {
		if key.Matches(msg, keys.Up) {
			if m.cursor > 0 {
				m.cursor--
				m.ensureCursorVisible()
			}
			return m.switchPreview()
		}
		if key.Matches(msg, keys.Down) {
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				m.ensureCursorVisible()
			}
			return m.switchPreview()
		}
	}

	// Enter
	if key.Matches(msg, keys.Enter) {
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			// Attach to session
			m.AttachTarget = m.preview.FullName
			m.AttachHost = m.preview.Host
			m.preview = nil
			m.quitting = true
			return m, tea.Quit
		}
		// Send text to session
		exec := m.findExecutor(m.preview.Host)
		_ = exec.SendKeys(m.preview.FullName, text)
		m.input.SetValue("")
		return m, m.capturePreviewCmd(m.preview.FullName, m.preview.Host)
	}

	// Default: update text input (no filtering in preview mode)
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) switchPreview() (tea.Model, tea.Cmd) {
	sel := m.selectedSession()
	if sel == nil {
		return m, nil
	}
	m.preview.SessionName = sel.Name
	m.preview.FullName = sel.FullName
	m.preview.Host = sel.Host
	m.preview.Output = ""
	return m, m.capturePreviewCmd(sel.FullName, sel.Host)
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Ignore all mouse events in preview mode
	if m.preview != nil {
		return m, nil
	}

	// Normal mode: scroll wheel navigates sessions
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.cursor > 0 {
			m.cursor--
			m.ensureCursorVisible()
		}
		return m, nil
	case tea.MouseButtonWheelDown:
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.ensureCursorVisible()
		}
		return m, nil
	}

	return m, nil
}

func (m Model) executeKill() (Model, tea.Cmd) {
	if m.confirmKill == nil {
		return m, nil
	}
	exec := m.findExecutor(m.confirmKill.Host)
	_ = exec.KillSession(m.confirmKill.FullName)
	m.confirmKill = nil
	m.preview = nil
	return m, m.refreshLocalSessions
}

func (m *Model) applyFilter() {
	query := strings.TrimSpace(m.input.Value())
	// Don't filter when typing a command (starts with /)
	if query == "" || strings.HasPrefix(query, "/") {
		m.filtered = m.sessions
	} else {
		lower := strings.ToLower(query)
		m.filtered = nil
		for _, s := range m.sessions {
			if strings.Contains(strings.ToLower(s.Name), lower) {
				m.filtered = append(m.filtered, s)
			}
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
	m.ensureCursorVisible()
}

func (m Model) maxVisibleSessions() int {
	if m.preview == nil {
		return len(m.filtered)
	}
	maxVis := m.height / 10
	if maxVis < 5 {
		maxVis = 5
	}
	if maxVis > len(m.filtered) {
		maxVis = len(m.filtered)
	}
	return maxVis
}

func (m *Model) ensureCursorVisible() {
	maxVis := m.maxVisibleSessions()
	if maxVis <= 0 {
		m.scrollOffset = 0
		return
	}
	if m.cursor < m.scrollOffset {
		m.scrollOffset = m.cursor
	}
	if m.cursor >= m.scrollOffset+maxVis {
		m.scrollOffset = m.cursor - maxVis + 1
	}
	// Clamp scrollOffset
	maxOffset := len(m.filtered) - maxVis
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
}

// filterByHost returns sessions that are remote (non-empty host) or local (empty host).
func filterByHost(sessions []session.Session, remoteOnly bool) []session.Session {
	var out []session.Session
	for _, s := range sessions {
		isRemote := s.Host != ""
		if isRemote == remoteOnly {
			out = append(out, s)
		}
	}
	return out
}

func (m Model) hasRemoteHosts() bool {
	for _, e := range m.executors {
		if e.HostName() != "" {
			return true
		}
	}
	return false
}

func (m Model) selectedSession() *session.Session {
	if len(m.filtered) == 0 {
		return nil
	}
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return nil
	}
	s := m.filtered[m.cursor]
	return &s
}

func parseNewCommand(text string) tea.Cmd {
	if !strings.HasPrefix(text, "/new ") {
		return nil
	}
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return nil
	}
	name := parts[1]
	if !validName.MatchString(name) {
		return nil
	}

	dir := ""
	if len(parts) >= 3 {
		dir = parts[2]
	}

	return func() tea.Msg {
		workDir := dir
		if workDir == "" {
			workDir, _ = os.Getwd()
		}

		fullName := tmux.SessionPrefix + name
		if tmux.HasSession(fullName) {
			return sessionCreatedMsg{Name: name, Err: fmt.Errorf("session %q already exists", name)}
		}

		claudeArgs := []string{"--dangerously-skip-permissions"}
		err := tmux.NewSession(name, workDir, claudeArgs)
		return sessionCreatedMsg{Name: name, Err: err}
	}
}

// cleanPreviewOutput strips Claude's TUI decoration from captured pane output.
func cleanPreviewOutput(output string) string {
	lines := strings.Split(output, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines at the start
		if len(cleaned) == 0 && trimmed == "" {
			continue
		}

		// Skip status bar lines
		if strings.Contains(trimmed, "bypass permissions") ||
			strings.Contains(trimmed, "shift+tab") ||
			strings.Contains(trimmed, "auto-accept") ||
			strings.Contains(trimmed, "plan mode") ||
			strings.Contains(trimmed, "esc to interrupt") ||
			strings.Contains(trimmed, "for shortcuts") {
			continue
		}

		// Skip box-drawing borders (╭, ╰)
		if strings.HasPrefix(trimmed, "╭") ||
			strings.HasPrefix(trimmed, "╰") {
			continue
		}

		// Skip pure horizontal rules
		if trimmed != "" && strings.TrimLeft(trimmed, "─") == "" {
			continue
		}

		cleaned = append(cleaned, line)
	}

	// Trim trailing empty lines
	for len(cleaned) > 0 && strings.TrimSpace(cleaned[len(cleaned)-1]) == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}

	return strings.Join(cleaned, "\n")
}
