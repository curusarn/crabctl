package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/simon/crabctl/internal/session"
	"github.com/simon/crabctl/internal/tmux"
)

const pollInterval = 1500 * time.Millisecond

type tickMsg time.Time

type previewOutputMsg struct {
	Output string
}

type previewState struct {
	SessionName string
	FullName    string
	Output      string
}

type confirmAction struct {
	SessionName string
	FullName    string
}

type Model struct {
	sessions      []session.Session
	filtered      []session.Session
	cursor        int
	input         textinput.Model
	preview       *previewState
	confirmKill   *confirmAction
	width, height int
	AttachTarget  string // set when user confirms attach
	quitting      bool
	err           error
}

func NewModel() Model {
	ti := textinput.New()
	ti.Placeholder = "Type to filter or enter command..."
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 60

	return Model{
		input: ti,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.refreshSessions,
		tickCmd(),
	)
}

func (m Model) refreshSessions() tea.Msg {
	sessions, err := session.List()
	if err != nil {
		return err
	}
	return sessions
}

func capturePreviewCmd(fullName string) tea.Cmd {
	return func() tea.Msg {
		output, err := tmux.CapturePaneOutput(fullName, 20)
		if err != nil {
			return previewOutputMsg{Output: "Error: " + err.Error()}
		}
		return previewOutputMsg{Output: cleanPreviewOutput(output)}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case []session.Session:
		m.sessions = msg
		if m.preview == nil {
			m.applyFilter()
		}
		return m, nil

	case error:
		m.err = msg
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{tickCmd(), m.refreshSessions}
		if m.preview != nil {
			cmds = append(cmds, capturePreviewCmd(m.preview.FullName))
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
			}
			return m, nil
		}
		if key.Matches(msg, keys.Down) {
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
			}
			return m, nil
		}
	}

	// Enter (empty): open preview
	if key.Matches(msg, keys.Enter) {
		sel := m.selectedSession()
		if sel == nil {
			return m, nil
		}
		m.preview = &previewState{
			SessionName: sel.Name,
			FullName:    sel.FullName,
		}
		m.input.SetValue("")
		return m, capturePreviewCmd(sel.FullName)
	}

	// Default: update text input and refilter
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.applyFilter()
	return m, cmd
}

func (m Model) handlePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	msgStr := msg.String()

	// Arrow keys always close preview and navigate
	// j/k only close preview when input is empty (otherwise they're text)
	if msgStr == "up" || (msgStr == "k" && m.input.Value() == "") {
		m.preview = nil
		m.input.SetValue("")
		m.applyFilter()
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	}
	if msgStr == "down" || (msgStr == "j" && m.input.Value() == "") {
		m.preview = nil
		m.input.SetValue("")
		m.applyFilter()
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
		return m, nil
	}

	// Enter
	if key.Matches(msg, keys.Enter) {
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			// Attach to session
			m.AttachTarget = m.preview.FullName
			m.preview = nil
			m.quitting = true
			return m, tea.Quit
		}
		// Send text to session
		_ = sendToSession(m.preview.FullName, text)
		m.input.SetValue("")
		return m, capturePreviewCmd(m.preview.FullName)
	}

	// Default: update text input (no filtering in preview mode)
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) executeKill() (Model, tea.Cmd) {
	if m.confirmKill == nil {
		return m, nil
	}
	_ = killSession(m.confirmKill.FullName)
	m.confirmKill = nil
	m.preview = nil
	return m, m.refreshSessions
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

func sendToSession(fullName, text string) error {
	return session.Send(fullName, text)
}

func killSession(fullName string) error {
	return session.Kill(fullName)
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
