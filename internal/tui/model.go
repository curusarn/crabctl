package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/simon/crabctl/internal/session"
	"github.com/simon/crabctl/internal/state"
	"github.com/simon/crabctl/internal/tmux"
)

const pollInterval = 1500 * time.Millisecond
const remotePollInterval = 5 * time.Second
const maxRemotePollInterval = 60 * time.Second
const spinnerInterval = 100 * time.Millisecond
const autoForwardDelay = 10 * time.Second
const maxAutoForwards = 5
// AutoForwardMessage is the message sent to sessions with autoforward enabled.
const AutoForwardMessage = `Continue working until done. Say "TASK_DONE!" (swap _ for space) if you really think you're done.`

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type tickMsg time.Time
type remoteTickMsg time.Time
type spinnerTickMsg time.Time

type sessionCreatedMsg struct {
	Name string
	Err  error
}

type sessionKilledMsg struct {
	Name string
}

// remoteSessionsMsg carries sessions from a single remote host.
// These get merged into the existing session list.
type remoteSessionsMsg struct {
	Host     string
	Sessions []session.Session
}

type autoForwardSentMsg struct {
	FullName string
}

type claudeSessionsMsg []session.ClaudeSession

type previewOutputMsg struct {
	FullName string
	Output   string
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
	WorkDir     string
	Killing     bool // true while kill is in progress
}

// RestoreState carries state between TUI restarts (after detaching from a session).
type RestoreState struct {
	FocusSession string            // name of session to re-focus
	Sessions     []session.Session // cached sessions to avoid blank screen
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
	remoteLoading  map[string]bool // hosts still being fetched (initial load)
	remoteFetching bool           // true while a remote refresh is in-flight
	spinnerFrame   int
	restore       *RestoreState
	store            *state.Store         // persistent state (nil-safe)
	// Auto-forward: automatically send "continue" when session waits
	autoForward      map[string]bool      // fullName -> enabled
	autoForwardCount map[string]int       // fullName -> consecutive forwards sent
	waitingSince     map[string]time.Time // fullName -> when first seen waiting
	// Resume mode: browse past Claude sessions to resume
	pendingFocus   string // full session name to focus+preview after resume
	resumeMode     bool
	resumeSessions []session.ClaudeSession
	resumeFiltered []session.ClaudeSession
	resumeCursor   int
	lastInteraction time.Time // last key/mouse event for remote backoff
	width, height   int
	AttachTarget    string // set when user confirms attach
	AttachHost      string // host of session to attach
	quitting       bool
	err            error
}

// GetRestoreState extracts state to carry over to the next TUI instance.
func (m Model) GetRestoreState() *RestoreState {
	focus := ""
	if sel := m.selectedSession(); sel != nil {
		focus = sel.FullName
	} else if m.AttachTarget != "" {
		focus = m.AttachTarget
	}
	return &RestoreState{
		FocusSession: focus,
		Sessions:     m.sessions,
	}
}

func NewModel(executors []tmux.Executor, restore *RestoreState, store *state.Store) Model {
	ti := textinput.New()
	ti.Placeholder = "Type to filter or enter command..."
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 4096
	ti.Width = 60

	loading := make(map[string]bool)
	for _, e := range executors {
		if e.HostName() != "" {
			loading[e.HostName()] = true
		}
	}

	m := Model{
		input:            ti,
		executors:        executors,
		remoteLoading:    loading,
		store:            store,
		autoForward:      make(map[string]bool),
		autoForwardCount: make(map[string]int),
		waitingSince:     make(map[string]time.Time),
		lastInteraction:  time.Now(),
	}

	// Load autoforward state from DB
	if store != nil {
		if af, err := store.LoadAllAutoForward(); err == nil {
			m.autoForward = af
		}
	}

	// Restore cached sessions and focus from previous TUI instance
	if restore != nil {
		m.restore = restore
		if len(restore.Sessions) > 0 {
			m.sessions = restore.Sessions
			m.filtered = restore.Sessions
			// Don't mark remote hosts as loading if we already have their sessions
			for _, s := range restore.Sessions {
				if s.Host != "" {
					delete(m.remoteLoading, s.Host)
				}
			}
		}
	}

	return m
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func remoteTickCmd(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return remoteTickMsg(t)
	})
}

// remoteInterval returns the remote poll interval based on inactivity.
// Doubles every 30s of inactivity, starting at 5s, capped at 60s.
func (m Model) remoteInterval() time.Duration {
	idle := time.Since(m.lastInteraction)
	interval := remotePollInterval
	for threshold := 30 * time.Second; interval < maxRemotePollInterval && idle >= threshold; threshold += 30 * time.Second {
		interval *= 2
	}
	if interval > maxRemotePollInterval {
		interval = maxRemotePollInterval
	}
	return interval
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
		m.refreshLocalSessions,
		tickCmd(),
	}
	if len(m.remoteLoading) > 0 {
		cmds = append(cmds, spinnerTickCmd())
		cmds = append(cmds, m.refreshRemoteSessions()...)
	}
	if m.hasRemoteHosts() {
		cmds = append(cmds, remoteTickCmd(remotePollInterval))
	}
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
			return previewOutputMsg{FullName: fullName, Output: "Error: " + err.Error()}
		}
		return previewOutputMsg{FullName: fullName, Output: cleanPreviewOutput(output)}
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

	case claudeSessionsMsg:
		m.resumeSessions = []session.ClaudeSession(msg)
		m.resumeMode = true
		m.resumeCursor = 0
		m.input.SetValue("")
		m.applyResumeFilter()
		return m, nil

	case sessionKilledMsg:
		m.confirmKill = nil
		m.preview = nil
		cmds := []tea.Cmd{m.refreshLocalSessions}
		cmds = append(cmds, m.refreshRemoteSessions()...)
		return m, tea.Batch(cmds...)

	case sessionCreatedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			m.pendingFocus = ""
		}
		m.input.SetValue("")
		m.resumeMode = false
		return m, m.refreshLocalSessions

	case []session.Session:
		// Local sessions replace only local entries, preserve remote
		remote := filterByHost(m.sessions, true)
		m.sessions = append(msg, remote...)
		session.SortSessions(m.sessions)
		prevFocus := m.focusedSessionName()
		m.applyFilter()
		if prevFocus != "" {
			m.focusSession(prevFocus)
		}
		// Restore focus on first refresh after restart
		if m.restore != nil {
			m.focusSession(m.restore.FocusSession)
			m.restore = nil
		}
		// Auto-focus + preview after resume
		if m.pendingFocus != "" {
			m.focusSession(m.pendingFocus)
			if sel := m.selectedSession(); sel != nil && sel.FullName == m.pendingFocus {
				m.preview = &previewState{
					SessionName: sel.Name,
					FullName:    sel.FullName,
					Host:        sel.Host,
				}
				m.pendingFocus = ""
				return m, m.capturePreviewCmd(sel.FullName, sel.Host)
			}
		}
		return m, nil

	case remoteSessionsMsg:
		// Clear loading/fetching state for this host
		delete(m.remoteLoading, msg.Host)
		m.remoteFetching = false
		// Replace sessions for this specific host, keep everything else
		var kept []session.Session
		for _, s := range m.sessions {
			if s.Host != msg.Host {
				kept = append(kept, s)
			}
		}
		m.sessions = append(kept, msg.Sessions...)
		session.SortSessions(m.sessions)
		prevFocus := m.focusedSessionName()
		m.applyFilter()
		if prevFocus != "" {
			m.focusSession(prevFocus)
		}
		return m, nil

	case error:
		m.err = msg
		return m, nil

	case spinnerTickMsg:
		m.spinnerFrame++
		needsSpinner := len(m.remoteLoading) > 0 || (m.confirmKill != nil && m.confirmKill.Killing)
		if needsSpinner {
			return m, spinnerTickCmd()
		}
		return m, nil

	case autoForwardSentMsg:
		m.autoForwardCount[msg.FullName]++
		return m, nil

	case tickMsg:
		m.syncAutoForwardFromDB()
		cmds := []tea.Cmd{tickCmd(), m.refreshLocalSessions}
		if m.preview != nil && !m.resumeMode {
			cmds = append(cmds, m.capturePreviewCmd(m.preview.FullName, m.preview.Host))
		}
		cmds = append(cmds, m.checkAutoForward()...)
		return m, tea.Batch(cmds...)

	case remoteTickMsg:
		cmds := []tea.Cmd{remoteTickCmd(m.remoteInterval())}
		if !m.remoteFetching && m.hasRemoteHosts() {
			m.remoteFetching = true
			cmds = append(cmds, m.refreshRemoteSessions()...)
		}
		return m, tea.Batch(cmds...)

	case previewOutputMsg:
		if m.preview != nil && m.preview.FullName == msg.FullName {
			m.preview.Output = msg.Output
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = msg.Width - 4
		return m, nil

	case tea.MouseMsg:
		wasIdle := m.remoteInterval() > remotePollInterval
		m.lastInteraction = time.Now()
		ret, cmd := m.handleMouse(msg)
		if wasIdle && m.hasRemoteHosts() && !m.remoteFetching {
			m.remoteFetching = true
			cmds := append(m.refreshRemoteSessions(), cmd)
			return ret, tea.Batch(cmds...)
		}
		return ret, cmd

	case tea.KeyMsg:
		wasIdle := m.remoteInterval() > remotePollInterval
		m.lastInteraction = time.Now()
		ret, cmd := m.handleKey(msg)
		if wasIdle && m.hasRemoteHosts() && !m.remoteFetching {
			m.remoteFetching = true
			cmds := append(m.refreshRemoteSessions(), cmd)
			return ret, tea.Batch(cmds...)
		}
		return ret, cmd
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
		if m.resumeMode {
			if m.preview != nil {
				m.preview = nil
				return m, nil
			}
			m.resumeMode = false
			m.input.SetValue("")
			m.applyFilter()
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

	// Ctrl+K: kill selected session (not in resume mode)
	if key.Matches(msg, keys.Kill) && !m.resumeMode {
		if sel := m.selectedSession(); sel != nil {
			m.confirmKill = &confirmAction{
				SessionName: sel.Name,
				FullName:    sel.FullName,
				Host:        sel.Host,
				WorkDir:     sel.WorkDir,
			}
		}
		return m, nil
	}

	// Ctrl+A: toggle autoforward on selected session
	if key.Matches(msg, keys.AutoForward) && !m.resumeMode {
		if sel := m.selectedSession(); sel != nil {
			m.ToggleAutoForward(sel.FullName)
		}
		return m, nil
	}

	// q quits only when input is empty and no preview/resume
	if key.Matches(msg, keys.Quit) && m.input.Value() == "" && m.preview == nil && !m.resumeMode {
		m.quitting = true
		return m, tea.Quit
	}

	// Resume mode key handling
	if m.resumeMode {
		return m.handleResumeKey(msg)
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

		// /resume command: browse killed sessions from DB
		if text == "/resume" || strings.HasPrefix(text, "/resume ") {
			store := m.store
			return m, func() tea.Msg {
				if store == nil {
					return claudeSessionsMsg(nil)
				}
				killed, err := store.ListKilled(100)
				if err != nil {
					return claudeSessionsMsg(nil)
				}
				sessions := make([]session.ClaudeSession, len(killed))
				for i, ks := range killed {
					sessions[i] = session.ClaudeSession{
						Name:         ks.Name,
						UUID:         ks.SessionUUID,
						ProjectDir:   ks.WorkDir,
						ModTime:      ks.KilledAt,
						FirstMessage: ks.FirstMsg,
					}
				}
				return claudeSessionsMsg(sessions)
			}
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
	// Resume mode: scroll wheel navigates claude sessions (with or without preview)
	if m.resumeMode {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.resumeCursor > 0 {
				m.resumeCursor--
			}
		case tea.MouseButtonWheelDown:
			if m.resumeCursor < len(m.resumeFiltered)-1 {
				m.resumeCursor++
			}
		}
		if m.preview != nil {
			return m.switchResumePreview()
		}
		return m, nil
	}

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
	m.confirmKill.Killing = true
	fullName := m.confirmKill.FullName
	host := m.confirmKill.Host
	name := m.confirmKill.SessionName
	workDir := m.confirmKill.WorkDir
	exec := m.findExecutor(host)
	store := m.store
	killCmd := func() tea.Msg {
		// Capture Claude session UUID before killing
		created := tmux.GetSessionCreated(fullName)
		uuid, firstMsg := session.FindSessionUUID(workDir, created)
		_ = exec.KillSession(fullName)
		// Record killed session in DB
		if store != nil && uuid != "" {
			store.MarkKilled(fullName, uuid, workDir, firstMsg)
		}
		return sessionKilledMsg{Name: name}
	}
	return m, tea.Batch(killCmd, spinnerTickCmd())
}

// checkAutoForward checks all sessions with autoforward enabled and sends
// the continue message if they've been waiting for longer than autoForwardDelay.
func (m *Model) checkAutoForward() []tea.Cmd {
	now := time.Now()
	var cmds []tea.Cmd

	// Update waitingSince tracking for all sessions
	activeFullNames := make(map[string]bool)
	for _, s := range m.sessions {
		activeFullNames[s.FullName] = true

		if !m.autoForward[s.FullName] {
			continue
		}

		isWaiting := s.Status == session.Waiting
		if isWaiting {
			if _, ok := m.waitingSince[s.FullName]; !ok {
				m.waitingSince[s.FullName] = now
			}
		} else {
			// Not waiting — reset timer
			delete(m.waitingSince, s.FullName)
			// Reset forward count when session starts running again
			if s.Status == session.Running {
				m.autoForwardCount[s.FullName] = 0
			}
		}

		// Don't auto-forward task-done sessions
		if s.Status == session.TaskDone {
			continue
		}

		// Check if we should forward
		since, ok := m.waitingSince[s.FullName]
		if !ok || now.Sub(since) < autoForwardDelay {
			continue
		}
		if m.autoForwardCount[s.FullName] >= maxAutoForwards {
			continue
		}

		// Send the continue message (re-check status first to avoid race)
		fullName := s.FullName
		host := s.Host
		exec := m.findExecutor(host)
		cmds = append(cmds, func() tea.Msg {
			// Re-capture pane to verify still waiting (not TaskDone)
			output, err := exec.CapturePaneOutput(fullName, 25)
			if err == nil {
				status := session.DetectStatus(output)
				if status != session.Waiting {
					return nil
				}
			}
			_ = exec.SendKeys(fullName, AutoForwardMessage)
			return autoForwardSentMsg{FullName: fullName}
		})
		// Reset timer so we wait another 10s
		m.waitingSince[s.FullName] = now
	}

	// Clean up tracking for sessions that no longer exist
	for fn := range m.waitingSince {
		if !activeFullNames[fn] {
			delete(m.waitingSince, fn)
			delete(m.autoForwardCount, fn)
		}
	}

	return cmds
}

// ToggleAutoForward toggles autoforward for the given session.
func (m *Model) ToggleAutoForward(fullName string) {
	if m.autoForward[fullName] {
		delete(m.autoForward, fullName)
		delete(m.waitingSince, fullName)
		delete(m.autoForwardCount, fullName)
		if m.store != nil {
			_ = m.store.SetAutoForward(fullName, false)
		}
	} else {
		m.autoForward[fullName] = true
		if m.store != nil {
			_ = m.store.SetAutoForward(fullName, true)
		}
	}
}

// SetAutoForward enables or disables autoforward for a session by name.
func (m *Model) SetAutoForward(fullName string, enabled bool) {
	if enabled {
		m.autoForward[fullName] = true
	} else {
		delete(m.autoForward, fullName)
		delete(m.waitingSince, fullName)
		delete(m.autoForwardCount, fullName)
	}
	if m.store != nil {
		_ = m.store.SetAutoForward(fullName, enabled)
	}
}

// syncAutoForwardFromDB merges DB state into the in-memory autoforward map.
// Newly enabled sessions are added, newly disabled sessions are removed.
// Runtime counters (autoForwardCount, waitingSince) are preserved for unchanged sessions.
func (m *Model) syncAutoForwardFromDB() {
	if m.store == nil {
		return
	}
	dbState, err := m.store.LoadAllAutoForward()
	if err != nil {
		return
	}

	// Add newly enabled sessions from DB
	for name := range dbState {
		m.autoForward[name] = true
	}

	// Remove sessions disabled in DB
	for name := range m.autoForward {
		if !dbState[name] {
			delete(m.autoForward, name)
			delete(m.waitingSince, name)
			delete(m.autoForwardCount, name)
		}
	}
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

// focusedSessionName returns the FullName of the currently focused session.
func (m Model) focusedSessionName() string {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return m.filtered[m.cursor].FullName
	}
	return ""
}

// focusSession moves the cursor to the session with the given fullName.
func (m *Model) focusSession(fullName string) {
	for i, s := range m.filtered {
		if s.FullName == fullName {
			m.cursor = i
			m.ensureCursorVisible()
			return
		}
	}
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
		} else if strings.HasPrefix(workDir, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				workDir = filepath.Join(home, workDir[2:])
			}
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

func (m Model) handleResumeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Navigation
	navigateUp := func() {
		if m.resumeCursor > 0 {
			m.resumeCursor--
		}
	}
	navigateDown := func() {
		if m.resumeCursor < len(m.resumeFiltered)-1 {
			m.resumeCursor++
		}
	}

	if m.input.Value() == "" {
		if key.Matches(msg, keys.Up) {
			navigateUp()
			if m.preview != nil {
				return m.switchResumePreview()
			}
			return m, nil
		}
		if key.Matches(msg, keys.Down) {
			navigateDown()
			if m.preview != nil {
				return m.switchResumePreview()
			}
			return m, nil
		}
	} else {
		// Arrow keys still navigate when filtering
		if msg.Type == tea.KeyUp {
			navigateUp()
			if m.preview != nil {
				return m.switchResumePreview()
			}
			return m, nil
		}
		if msg.Type == tea.KeyDown {
			navigateDown()
			if m.preview != nil {
				return m.switchResumePreview()
			}
			return m, nil
		}
	}

	// Enter: two-stage — first opens preview, second resumes
	if key.Matches(msg, keys.Enter) {
		sel := m.selectedClaudeSession()
		if sel == nil {
			return m, nil
		}

		// Stage 1: open preview
		if m.preview == nil {
			cs := *sel
			m.preview = &previewState{
				SessionName: strings.TrimPrefix(cs.Name, tmux.SessionPrefix),
				FullName:    cs.UUID,
			}
			return m, m.resumePreviewCmd(cs)
		}

		// Stage 2: resume session
		cs := *sel
		name := strings.TrimPrefix(cs.Name, tmux.SessionPrefix)
		fullName := tmux.SessionPrefix + name
		m.pendingFocus = fullName
		m.preview = nil
		return m, func() tea.Msg {
			if tmux.HasSession(fullName) {
				return sessionCreatedMsg{Name: name, Err: fmt.Errorf("session %q already exists", name)}
			}
			claudeArgs := []string{"--dangerously-skip-permissions", "--resume", cs.UUID}
			err := tmux.NewSession(name, cs.ProjectDir, claudeArgs)
			return sessionCreatedMsg{Name: name, Err: err}
		}
	}

	// Default: update text input and refilter
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.applyResumeFilter()
	return m, cmd
}

func (m Model) resumePreviewCmd(cs session.ClaudeSession) tea.Cmd {
	workDir := cs.ProjectDir
	uuid := cs.UUID
	return func() tea.Msg {
		output := session.ReadSessionPreview(workDir, uuid, 30)
		if output == "" {
			output = "(no conversation found)"
		}
		return previewOutputMsg{FullName: uuid, Output: output}
	}
}

func (m Model) switchResumePreview() (tea.Model, tea.Cmd) {
	sel := m.selectedClaudeSession()
	if sel == nil {
		return m, nil
	}
	cs := *sel
	m.preview.SessionName = strings.TrimPrefix(cs.Name, tmux.SessionPrefix)
	m.preview.FullName = cs.UUID
	m.preview.Output = ""
	return m, m.resumePreviewCmd(cs)
}

func (m *Model) applyResumeFilter() {
	query := strings.TrimSpace(m.input.Value())
	if query == "" {
		m.resumeFiltered = m.resumeSessions
	} else {
		lower := strings.ToLower(query)
		m.resumeFiltered = nil
		for _, cs := range m.resumeSessions {
			if strings.Contains(strings.ToLower(cs.Name), lower) ||
				strings.Contains(strings.ToLower(cs.ProjectDir), lower) ||
				strings.Contains(strings.ToLower(cs.FirstMessage), lower) {
				m.resumeFiltered = append(m.resumeFiltered, cs)
			}
		}
	}
	if m.resumeCursor >= len(m.resumeFiltered) {
		m.resumeCursor = max(0, len(m.resumeFiltered)-1)
	}
}

func (m Model) selectedClaudeSession() *session.ClaudeSession {
	if len(m.resumeFiltered) == 0 {
		return nil
	}
	if m.resumeCursor < 0 || m.resumeCursor >= len(m.resumeFiltered) {
		return nil
	}
	cs := m.resumeFiltered[m.resumeCursor]
	return &cs
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
