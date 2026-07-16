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
const maxRemoteFailures = 3
// AutoForwardMessage is the message sent to sessions with autoforward enabled.
const AutoForwardMessage = `Continue working until done. Say "TASK_DONE!" (swap _ for space) if you really think you're done.`

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type commandDef struct {
	Name        string // "/new"
	Usage       string // "/new <name> [dir]"
	Description string // "create a new session"
}

// OrchestratorName is the reserved session name for the singleton orchestrator.
// The orchestrator is always anchored at ~/git/crabctl.
const OrchestratorName = "orchestrator"

var commandDefs = []commandDef{
	{"/new", "/new [name] [dir]", "create a new session (no args = directory picker)"},
	{"/orchestrator", "/orchestrator", "spawn the singleton crab-orchestrator session"},
	{"/refresh", "/refresh", "force re-fetch all sessions and PR info"},
	{"/resume", "/resume", "browse and resume past Claude sessions"},
	{"/quit", "/quit", "quit crabctl"},
}

func matchingCommands(input string) []commandDef {
	cmd := strings.TrimSpace(input)
	if cmd == "/" {
		return commandDefs
	}
	if strings.Contains(cmd, " ") {
		return nil
	}
	var matches []commandDef
	for _, c := range commandDefs {
		if strings.HasPrefix(c.Name, cmd) {
			matches = append(matches, c)
		}
	}
	return matches
}

type tickMsg time.Time
type remoteTickMsg time.Time
type spinnerTickMsg time.Time
type refreshMsg struct{}

type prResolvedMsg struct {
	FullName string
	Host     string
	PR       string
	PRURL    string
	PRState  string
}

type sessionCreatedMsg struct {
	Name string
	Err  error
}

type sessionKilledMsg struct {
	Name     string
	FullName string
	Host     string
	Killed   []killTarget // all targets that were killed
}

// remoteSessionsMsg carries sessions from a single remote host.
// These get merged into the existing session list.
type remoteSessionsMsg struct {
	Host     string
	Sessions []session.Session
	Err      error // non-nil if SSH fetch failed
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

type killTarget struct {
	Name, FullName, Host, WorkDir string
	SessionUUID, SessionFirstMsg  string
}

type confirmAction struct {
	Targets []killTarget
	Killing bool // true while kill is in progress
}

// RestoreState carries state between TUI restarts (after detaching from a session).
type RestoreState struct {
	FocusSession   string               // name of session to re-focus
	Sessions       []session.Session    // cached sessions to avoid blank screen
	LastInteracted map[string]time.Time // interaction times survive attach/detach
}

type Model struct {
	sessions      []session.Session
	filtered      []session.Session
	cursor        int
	scrollOffset  int
	selected      map[string]bool // multi-select by FullName
	suggestCursor int
	input         textinput.Model
	preview       *previewState
	confirmKill   *confirmAction
	executors     []tmux.Executor
	remoteLoading  map[string]bool // hosts still being fetched (initial load)
	remoteFetching bool           // true while a remote refresh is in-flight
	remoteFailures map[string]int  // host → consecutive SSH failure count
	remoteFailed   map[string]bool // hosts that hit the retry limit
	spinnerFrame   int
	restore       *RestoreState
	store            *state.Store         // persistent state (nil-safe)
	// Auto-forward: automatically send "continue" when session waits
	autoForward      map[string]bool      // fullName -> enabled
	autoForwardCount map[string]int       // fullName -> consecutive forwards sent
	waitingSince     map[string]time.Time // fullName -> when first seen waiting
	// Stored session UUIDs from DB (loaded once on startup, used in mergeSessionState)
	storedUUIDs    map[string][3]string // session name → [uuid, workDir, firstMsg]
	// Parent-child hierarchy
	parents        map[string]string    // session key → parent key (from DB)
	foldState      map[string]int       // session key → fold state (per-session Ctrl+H)
	lastInteracted map[string]time.Time // session key → last attach/send time
	// Resume mode: browse past Claude sessions to resume
	pendingFocus   string // full session name to focus+preview after resume
	dirPicker      *dirPickerState // non-nil when dir-picker overlay is open
	resumeMode     bool
	resumeSessions []session.ClaudeSession
	resumeFiltered []session.ClaudeSession
	resumeCursor   int
	refreshPending  bool      // true while a user-initiated refresh is in-flight
	lastInteraction time.Time // last key/mouse event for remote backoff
	// Rotating-hint footer state
	hintIndex        int
	lastHintRotation time.Time
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
		FocusSession:   focus,
		Sessions:       m.sessions,
		LastInteracted: m.lastInteracted,
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
		selected:         make(map[string]bool),
		remoteLoading:    loading,
		remoteFailures:   make(map[string]int),
		remoteFailed:     make(map[string]bool),
		store:            store,
		autoForward:      make(map[string]bool),
		autoForwardCount: make(map[string]int),
		waitingSince:     make(map[string]time.Time),
		lastInteraction:  time.Now(),
	}

	// Load state from DB
	if store != nil {
		if af, err := store.LoadAllAutoForward(); err == nil {
			m.autoForward = af
		}
		if prs, err := store.LoadAllPRs(); err == nil {
			session.WarmPRCache(prs)
		}
		if parents, err := store.LoadAllParents(); err == nil {
			m.parents = parents
		}
		if li, err := store.LoadAllInteractions(); err == nil {
			m.lastInteracted = li
		}
		if uuids, err := store.LoadAllSessionUUIDs(); err == nil {
			m.storedUUIDs = uuids
		}
	}
	if m.parents == nil {
		m.parents = make(map[string]string)
	}
	if m.lastInteracted == nil {
		m.lastInteracted = make(map[string]time.Time)
	}
	m.foldState = make(map[string]int)

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
		// Merge restored interaction times (in-memory updates from previous instance)
		for k, t := range restore.LastInteracted {
			if t.After(m.lastInteracted[k]) {
				m.lastInteracted[k] = t
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
// Hosts that have hit the retry limit are skipped.
func (m Model) refreshRemoteSessions() []tea.Cmd {
	var cmds []tea.Cmd
	for _, ex := range m.executors {
		if ex.HostName() != "" {
			if m.remoteFailed[ex.HostName()] {
				continue // skip hosts that hit retry limit
			}
			ex := ex // capture
			cmds = append(cmds, func() tea.Msg {
				sessions, err := session.ListExecutor(ex)
				return remoteSessionsMsg{
					Host:     ex.HostName(),
					Sessions: sessions,
					Err:      err,
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

	case refreshMsg:
		return m, m.performRefresh()

	case prResolvedMsg:
		for i := range m.sessions {
			if m.sessions[i].FullName == msg.FullName && m.sessions[i].Host == msg.Host {
				if msg.PR != "" {
					m.sessions[i].PR = msg.PR
				}
				m.sessions[i].PRURL = msg.PRURL
				m.sessions[i].PRState = msg.PRState
				// Persist to DB
				if msg.PRURL != "" && m.store != nil {
					m.store.SavePR(session.SessionKey(msg.Host, msg.FullName), msg.PRURL, msg.PRState)
				}
				break
			}
		}
		m.applyFilter()
		return m, nil

	case claudeSessionsMsg:
		m.resumeSessions = []session.ClaudeSession(msg)
		m.resumeMode = true
		m.resumeCursor = 0
		m.selected = make(map[string]bool)
		m.input.SetValue("")
		m.applyResumeFilter()
		return m, nil

	case sessionKilledMsg:
		m.confirmKill = nil
		m.selected = make(map[string]bool)
		// Only clear preview if the killed session was the one being previewed
		if m.preview != nil && m.preview.FullName == msg.FullName && m.preview.Host == msg.Host {
			m.preview = nil
		}
		// Immediately remove killed sessions from the list
		killed := make(map[string]bool, len(msg.Killed))
		for _, t := range msg.Killed {
			killed[t.Host+"\x00"+t.FullName] = true
		}
		alive := m.sessions[:0]
		for _, s := range m.sessions {
			if !killed[s.Host+"\x00"+s.FullName] {
				alive = append(alive, s)
			}
		}
		m.sessions = alive
		m.applyFilter()
		// Still refresh to pick up any other changes
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
		// Reload parents from DB (new session may have a parent)
		if m.store != nil {
			if parents, err := m.store.LoadAllParents(); err == nil {
				m.parents = parents
			}
		}
		// Treat a successful spawn as an interaction so the new session
		// bubbles to the top of the sort (BuildTree orders by lastInteracted).
		if msg.Err == nil && msg.Name != "" {
			fullName := tmux.SessionPrefix + msg.Name
			m.recordInteraction(fullName, "")
		}
		return m, m.refreshLocalSessions

	case []session.Session:
		m.err = nil
		// During a user-initiated refresh, clear carried-forward PR URLs
		// so they're re-resolved from scratch (PR cache was already cleared).
		// Sessions stay visible to avoid a flash.
		if m.refreshPending {
			for i := range m.sessions {
				m.sessions[i].PRURL = ""
				m.sessions[i].PRState = ""
			}
			m.refreshPending = false
		}
		// Carry forward already-resolved session state (UUIDs, PR URLs)
		m.mergeSessionState(msg)
		// Auto-persist parents discovered from tmux env (CRABCTL_PARENT)
		m.persistDiscoveredParents(msg)
		// Local sessions replace only local entries, preserve remote
		remote := filterByHost(m.sessions, true)
		local := filterByHost(m.sessions, false)
		// Don't replace a populated list with an empty one (transient tmux hiccup)
		if len(msg) == 0 && len(local) > 0 {
			return m, nil
		}
		m.sessions = append(msg, remote...)
		m.sessions = session.BuildTree(m.sessions, m.parents, m.foldState, m.lastInteracted)
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
		// Collect commands: lazy PR resolution + any focus/preview
		var cmds []tea.Cmd
		if prCmd := m.resolvePRsCmd(); prCmd != nil {
			cmds = append(cmds, prCmd)
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
				cmds = append(cmds, m.capturePreviewCmd(sel.FullName, sel.Host))
			}
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)

	case remoteSessionsMsg:
		m.remoteFetching = false
		// Track SSH failures — keep spinner running during retries
		if msg.Err != nil {
			m.remoteFailures[msg.Host]++
			if m.remoteFailures[msg.Host] >= maxRemoteFailures {
				m.remoteFailed[msg.Host] = true
				delete(m.remoteLoading, msg.Host) // stop spinner on final failure
			}
			return m, nil
		}
		// Success — clear loading and reset failure counter
		delete(m.remoteLoading, msg.Host)
		delete(m.remoteFailures, msg.Host)
		delete(m.remoteFailed, msg.Host)
		// Replace sessions for this specific host, keep everything else
		var kept []session.Session
		var oldHostCount int
		for _, s := range m.sessions {
			if s.Host != msg.Host {
				kept = append(kept, s)
			} else {
				oldHostCount++
			}
		}
		// Don't replace a populated list with an empty one (transient hiccup)
		if len(msg.Sessions) == 0 && oldHostCount > 0 {
			return m, nil
		}
		// Carry forward already-resolved session state (UUIDs, PR URLs)
		m.mergeSessionState(msg.Sessions)
		m.sessions = append(kept, msg.Sessions...)
		// Auto-persist parents discovered from tmux env (CRABCTL_PARENT)
		m.persistDiscoveredParents(msg.Sessions)
		m.sessions = session.BuildTree(m.sessions, m.parents, m.foldState, m.lastInteracted)
		prevFocus := m.focusedSessionName()
		m.applyFilter()
		if prevFocus != "" {
			m.focusSession(prevFocus)
		}
		if prCmd := m.resolvePRsCmd(); prCmd != nil {
			return m, prCmd
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
		m.rotateHintIfDue()
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
		if m.dirPicker != nil {
			return m.handleDirPickerKey(msg)
		}
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

	// If kill is in progress (spinner), ignore all keys
	if m.confirmKill != nil && m.confirmKill.Killing {
		return m, nil
	}

	// If kill confirmation is pending, only Enter proceeds, only Escape cancels
	if m.confirmKill != nil {
		if key.Matches(msg, keys.Enter) {
			return m.executeKill()
		}
		if key.Matches(msg, keys.Escape) {
			m.confirmKill = nil
		}
		return m, nil
	}

	// Ctrl+K: kill selected session(s) (not in resume mode)
	if key.Matches(msg, keys.Kill) && !m.resumeMode {
		if len(m.selected) > 0 {
			var targets []killTarget
			for _, s := range m.filtered {
				if m.selected[s.FullName] {
					targets = append(targets, killTarget{
						Name:            s.Name,
						FullName:        s.FullName,
						Host:            s.Host,
						WorkDir:         s.WorkDir,
						SessionUUID:     s.SessionUUID,
						SessionFirstMsg: s.SessionFirstMsg,
					})
				}
			}
			if len(targets) > 0 {
				m.confirmKill = &confirmAction{Targets: targets}
			}
		} else if sel := m.selectedSession(); sel != nil {
			m.confirmKill = &confirmAction{
				Targets: []killTarget{{
					Name:            sel.Name,
					FullName:        sel.FullName,
					Host:            sel.Host,
					WorkDir:         sel.WorkDir,
					SessionUUID:     sel.SessionUUID,
					SessionFirstMsg: sel.SessionFirstMsg,
				}},
			}
		}
		return m, nil
	}

	// Ctrl+R: refresh all sessions and caches (not in resume mode)
	if key.Matches(msg, keys.Refresh) && !m.resumeMode {
		return m, m.performRefresh()
	}

	// Ctrl+A: toggle autoforward on selected session
	if key.Matches(msg, keys.AutoForward) && !m.resumeMode {
		if sel := m.selectedSession(); sel != nil {
			m.ToggleAutoForward(sel.FullName)
		}
		return m, nil
	}

	// Ctrl+H: toggle fold state (show all / hide all children)
	if key.Matches(msg, keys.HideChildren) && !m.resumeMode {
		if sel := m.selectedSession(); sel != nil {
			selKey := session.SessionKey(sel.Host, sel.FullName)
			// Check if session has children
			hasChildren := false
			for _, parentKey := range m.parents {
				if parentKey == selKey {
					hasChildren = true
					break
				}
			}
			// If focused session has children, toggle its own fold state.
			// Otherwise, toggle its parent's fold state and move cursor to parent.
			targetKey := selKey
			focusParent := false
			if !hasChildren {
				targetKey = m.parents[selKey]
				focusParent = true
			}
			if targetKey != "" {
				if m.foldState[targetKey] == session.FoldClosed {
					delete(m.foldState, targetKey)
				} else {
					m.foldState[targetKey] = session.FoldClosed
				}
				m.sessions = session.BuildTree(m.sessions, m.parents, m.foldState, m.lastInteracted)
				m.applyFilter()
				if focusParent {
					for i, s := range m.filtered {
						k := session.SessionKey(s.Host, s.FullName)
						if k == targetKey {
							m.cursor = i
							break
						}
					}
				}
			}
		}
		return m, nil
	}

	// Dir-picker overlay takes precedence over all other modes
	if m.dirPicker != nil {
		return m.handleDirPickerKey(msg)
	}

	// Ctrl+N opens the dir-picker (or, in feature 2, the orchestrator quick-spawn)
	if key.Matches(msg, keys.NewSession) && !m.resumeMode {
		return m.openSpawnFlow()
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
	// Command suggestion navigation: when typing a /command prefix
	text := strings.TrimSpace(m.input.Value())
	if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
		matches := matchingCommands(text)
		if len(matches) > 0 {
			if key.Matches(msg, keys.Up) {
				if m.suggestCursor > 0 {
					m.suggestCursor--
				}
				return m, nil
			}
			if key.Matches(msg, keys.Down) {
				if m.suggestCursor < len(matches)-1 {
					m.suggestCursor++
				}
				return m, nil
			}
		}
	}

	// Arrow keys navigate the filtered session list regardless of input
	// content (a /command-suggestion menu is handled above and returns early).
	// Without this, typing a filter would trap the user — they could narrow
	// the list but not pick from it without first clearing the filter.
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

	// Multi-select with Space stays gated to empty input — otherwise Space
	// would be a filter character.
	if m.input.Value() == "" {
		if key.Matches(msg, keys.Space) {
			if sel := m.selectedSession(); sel != nil {
				if m.selected[sel.FullName] {
					delete(m.selected, sel.FullName)
				} else {
					m.selected[sel.FullName] = true
				}
				// Advance cursor down (like file managers)
				if m.cursor < len(m.filtered)-1 {
					m.cursor++
					m.ensureCursorVisible()
				}
			}
			return m, nil
		}
	}

	// Tab: complete to selected matching command
	if key.Matches(msg, keys.Tab) {
		text := strings.TrimSpace(m.input.Value())
		if strings.HasPrefix(text, "/") {
			matches := matchingCommands(text)
			idx := m.suggestCursor
			if idx >= len(matches) {
				idx = 0
			}
			if len(matches) > 0 && text != matches[idx].Name {
				m.input.SetValue(matches[idx].Name + " ")
				m.input.CursorEnd()
				m.suggestCursor = 0
			}
		}
		return m, nil
	}

	// Enter
	if key.Matches(msg, keys.Enter) {
		text := strings.TrimSpace(m.input.Value())

		// Resolve partial command to selected suggestion
		if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
			matches := matchingCommands(text)
			idx := m.suggestCursor
			if idx >= len(matches) {
				idx = 0
			}
			if len(matches) > 0 && text != matches[idx].Name {
				text = matches[idx].Name
			}
		}

		// /quit command
		if text == "/quit" {
			m.quitting = true
			return m, tea.Quit
		}

		// /new with no args routes through openSpawnFlow (picker or orchestrator).
		if text == "/new" {
			m.input.SetValue("")
			return m.openSpawnFlow()
		}

		// /orchestrator spawns the reserved singleton crab-orchestrator.
		if text == "/orchestrator" {
			m.input.SetValue("")
			return m, m.spawnOrchestratorCmd()
		}

		// /new command: create a new session
		if cmd := m.parseNewCommand(text); cmd != nil {
			m.input.SetValue("")
			return m, cmd
		}

		// /refresh command: force re-fetch all sessions and PR info
		if text == "/refresh" {
			m.input.SetValue("")
			return m, m.performRefresh()
		}

		// /resume command: browse past sessions from DB
		if text == "/resume" {
			store := m.store
			executors := m.executors
			return m, func() tea.Msg {
				if store == nil {
					return claudeSessionsMsg(nil)
				}
				past, err := store.ListResumable(100)
				if err != nil {
					return claudeSessionsMsg(nil)
				}
				// Collect active session names to filter them out
				active := make(map[string]bool)
				for _, ex := range executors {
					infos, _ := ex.ListSessions()
					for _, info := range infos {
						active[info.FullName] = true
					}
				}
				var sessions []session.ClaudeSession
				for _, ps := range past {
					if active[ps.Name] {
						continue
					}
					sessions = append(sessions, session.ClaudeSession{
						Name:         ps.Name,
						UUID:         ps.SessionUUID,
						ProjectDir:   ps.WorkDir,
						ModTime:      ps.LastSeen,
						FirstMessage: ps.FirstMsg,
						Killed:       ps.Killed,
					})
				}
				return claudeSessionsMsg(sessions)
			}
		}

		// Open preview
		sel := m.selectedSession()
		if sel == nil || sel.Virtual {
			return m, nil
		}
		m.selected = make(map[string]bool)
		m.preview = &previewState{
			SessionName: sel.Name,
			FullName:    sel.FullName,
			Host:        sel.Host,
		}
		m.input.SetValue("")
		return m, m.capturePreviewCmd(sel.FullName, sel.Host)
	}

	// Default: update text input and refilter
	prev := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != prev {
		m.suggestCursor = 0
	}
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
			m.recordInteraction(m.preview.FullName, m.preview.Host)
			m.AttachTarget = m.preview.FullName
			m.AttachHost = m.preview.Host
			m.preview = nil
			m.quitting = true
			return m, tea.Quit
		}
		// Send text to session — intentionally NOT recorded as an
		// interaction. Send is typically the orchestrator delegating to a
		// worker, and we don't want the worker to bubble above the
		// orchestrator in the sort. Attach (above) still counts.
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
	if sel.Virtual {
		m.preview.Output = "(no active session)"
		return m, nil
	}
	m.preview.Output = ""
	return m, m.capturePreviewCmd(sel.FullName, sel.Host)
}

// recordInteraction saves an interaction timestamp for a session (in-memory + DB).
func (m *Model) recordInteraction(fullName, host string) {
	key := session.SessionKey(host, fullName)
	m.lastInteracted[key] = time.Now().UTC()
	if m.store != nil {
		_ = m.store.SaveInteraction(key)
	}
}

func (m Model) executeKill() (Model, tea.Cmd) {
	if m.confirmKill == nil || len(m.confirmKill.Targets) == 0 {
		return m, nil
	}
	m.confirmKill.Killing = true
	targets := m.confirmKill.Targets
	store := m.store
	// Collect claimed UUIDs so kill-path resolution doesn't steal another session's UUID
	claimed := make(map[string]bool)
	for _, s := range m.sessions {
		if s.SessionUUID != "" {
			claimed[s.SessionUUID] = true
		}
	}
	// Use the last target for the completion message (triggers refresh)
	last := targets[len(targets)-1]
	killCmd := func() tea.Msg {
		for _, t := range targets {
			exec := m.findExecutor(t.Host)
			uuid, firstMsg := t.SessionUUID, t.SessionFirstMsg
			if uuid == "" {
				paneContent, _ := exec.CapturePaneOutput(t.FullName, 50)
				created := tmux.GetSessionCreated(t.FullName)
				uuid, firstMsg = session.FindSessionUUID(t.WorkDir, created, paneContent, claimed)
			}
			_ = exec.KillSession(t.FullName)
			if store != nil && uuid != "" {
				store.MarkKilled(t.FullName, uuid, t.WorkDir, firstMsg)
			}
		}
		return sessionKilledMsg{Name: last.Name, FullName: last.FullName, Host: last.Host, Killed: targets}
	}
	return m, tea.Batch(killCmd, spinnerTickCmd())
}

// mergeSessionState carries forward already-resolved UUIDs and PR URLs
// from old sessions, resolving new ones only when first discovered.
func (m *Model) mergeSessionState(sessions []session.Session) {
	// Build lookup from existing sessions (keyed by host:fullName for uniqueness)
	known := make(map[string]session.Session)
	for _, s := range m.sessions {
		known[session.SessionKey(s.Host, s.FullName)] = s
	}

	// Collect all claimed UUIDs so new resolutions skip already-matched files
	claimed := make(map[string]bool)
	for _, s := range m.sessions {
		if s.SessionUUID != "" {
			claimed[s.SessionUUID] = true
		}
	}

	for i := range sessions {
		s := &sessions[i]
		sKey := session.SessionKey(s.Host, s.FullName)
		if old, ok := known[sKey]; ok {
			// Carry forward stable project directory from first discovery.
			// Pane's current path can change if Claude cd's.
			if old.WorkDir != "" {
				s.WorkDir = old.WorkDir
			}
			// Carry forward already-resolved UUID
			if old.SessionUUID != "" {
				s.SessionUUID = old.SessionUUID
				s.SessionFirstMsg = old.SessionFirstMsg
			}
			// Carry forward PRURL and PRState if the PR number hasn't changed.
			if old.PRURL != "" && (s.PR == "" || old.PR == s.PR) {
				s.PRURL = old.PRURL
				s.PRState = old.PRState
				if s.PR == "" {
					s.PR = old.PR
				}
			}
		}

		// Persist new PR URLs to DB
		if s.PRURL != "" && m.store != nil {
			if old, ok := known[sKey]; !ok || old.PRURL != s.PRURL {
				m.store.SavePR(sKey, s.PRURL, s.PRState)
			}
		}

		// Resolve UUID: file content matching (tool calls + user msgs vs pane),
		// then DB-stored, as strategies.
		if s.SessionUUID == "" {
			key := sKey

			// Strategy 1: match session file content against pane (local only)
			// Now includes tool call matching which is more reliable than
			// user messages alone (tool calls persist on screen longer).
			if s.Host == "" && s.WorkDir != "" {
				s.SessionUUID, s.SessionFirstMsg = session.FindSessionUUID(
					s.WorkDir, time.Now().Add(-s.Duration), s.PaneContent, claimed,
				)
				if s.SessionUUID != "" {
					claimed[s.SessionUUID] = true
					if m.store != nil {
						m.store.SaveSessionUUID(key, s.SessionUUID, s.WorkDir, s.SessionFirstMsg)
					}
				}
			}

			// Strategy 2: use DB-stored UUID (from send/detach history resolution)
			// Validate that the session file has been modified during this
			// tmux session's lifetime — stale/missing files are not trusted.
			if s.SessionUUID == "" {
				if stored, ok := m.storedUUIDs[key]; ok && stored[0] != "" {
					storedWorkDir := stored[1]
					if storedWorkDir == "" {
						storedWorkDir = s.WorkDir
					}
					modTime := session.SessionFileModTime(storedWorkDir, stored[0])
					sessionCreated := time.Now().Add(-s.Duration)
					if !modTime.IsZero() && modTime.After(sessionCreated) {
						s.SessionUUID = stored[0]
						if s.WorkDir == "" {
							s.WorkDir = stored[1]
						}
						if s.SessionFirstMsg == "" {
							s.SessionFirstMsg = stored[2]
						}
						claimed[s.SessionUUID] = true
					}
				}
			}
		}

		// Compute LastActive from the known session file (single stat call)
		if s.SessionUUID != "" && s.Host == "" {
			s.LastActive = session.SessionFileModTime(s.WorkDir, s.SessionUUID)
		}

		s.PaneContent = "" // no longer needed after UUID resolution
	}
}

// persistDiscoveredParents saves parent relationships discovered from tmux
// env vars (CRABCTL_PARENT) to the in-memory map and DB on first discovery.
func (m *Model) persistDiscoveredParents(sessions []session.Session) {
	for _, s := range sessions {
		if s.Parent == "" {
			continue
		}
		key := session.SessionKey(s.Host, s.FullName)
		if m.parents[key] == "" {
			m.parents[key] = s.Parent
			if m.store != nil {
				m.store.SaveParent(key, s.Parent)
			}
		}
	}
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
	// Don't filter when typing a command or when in preview mode
	// (in preview mode the input is for sending messages, not filtering)
	if query == "" || strings.HasPrefix(query, "/") || query == "?" || m.preview != nil {
		m.filtered = nil
		for _, s := range m.sessions {
			if !s.TreeHidden {
				m.filtered = append(m.filtered, s)
			}
		}
	} else {
		lower := strings.ToLower(query)
		m.filtered = nil
		for _, s := range m.sessions {
			if !s.TreeHidden && strings.Contains(strings.ToLower(s.Name), lower) {
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

// hintRotationInterval controls how often the rotating footer hint advances.
const hintRotationInterval = 6 * time.Second

// rotateHintIfDue advances hintIndex if enough time has elapsed since the
// last rotation. Called from the regular tick handler — drift is bounded by
// pollInterval (1.5s).
func (m *Model) rotateHintIfDue() {
	if m.lastHintRotation.IsZero() {
		m.lastHintRotation = time.Now()
		return
	}
	if time.Since(m.lastHintRotation) >= hintRotationInterval {
		if n := len(rotatingHints); n > 0 {
			m.hintIndex = (m.hintIndex + 1) % n
		}
		m.lastHintRotation = time.Now()
	}
}

// openSpawnFlow routes Ctrl+N (or `/new`-no-args) to the right action:
//   - if no sessions exist yet → spawn the orchestrator directly
//   - otherwise → open the directory picker
//
// One shortcut, two behaviors, so users only learn ctrl+n.
func (m Model) openSpawnFlow() (tea.Model, tea.Cmd) {
	if len(m.sessions) == 0 {
		return m, m.spawnOrchestratorCmd()
	}
	startDir := ""
	parent := ""
	if sel := m.selectedSession(); sel != nil {
		startDir = sel.WorkDir
		parent = sel.FullName
	}
	m.preview = nil
	m.input.SetValue("")
	m.dirPicker = openDirPicker(startDir)
	m.dirPicker.Parent = parent
	return m, nil
}

// spawnOrchestratorCmd builds the tea.Cmd that creates the reserved
// crab-orchestrator session anchored at ~/git/crabctl. Singleton — errors
// out if it already exists.
func (m Model) spawnOrchestratorCmd() tea.Cmd {
	store := m.store
	parent := tmux.DetectParent("")
	return func() tea.Msg {
		fullName := tmux.SessionPrefix + OrchestratorName
		if tmux.HasSession(fullName) {
			return sessionCreatedMsg{Name: OrchestratorName, Err: fmt.Errorf("orchestrator already running")}
		}
		dir := orchestratorDir()
		claudeArgs := []string{"--dangerously-skip-permissions"}
		err := tmux.NewSession(OrchestratorName, dir, claudeArgs, parent)
		if err == nil && parent != "" && store != nil {
			store.SaveParent(session.SessionKey("", fullName), parent)
		}
		return sessionCreatedMsg{Name: OrchestratorName, Err: err}
	}
}

// orchestratorDir resolves ~/git/crabctl, falling back to $HOME, then cwd.
func orchestratorDir() string {
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "git", "crabctl")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		return home
	}
	cwd, _ := os.Getwd()
	return cwd
}

func (m Model) parseNewCommand(text string) tea.Cmd {
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

	parent := tmux.DetectParent("")
	store := m.store

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
		err := tmux.NewSession(name, workDir, claudeArgs, parent)
		if err == nil && parent != "" && store != nil {
			sessionKey := session.SessionKey("", fullName)
			store.SaveParent(sessionKey, parent)
		}
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
			err := tmux.NewSession(name, cs.ProjectDir, claudeArgs, "")
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

// performRefresh clears all cached state and triggers a full re-fetch.
// Sessions are kept visible until new data arrives (no flash).
func (m *Model) performRefresh() tea.Cmd {
	m.refreshPending = true
	// Clear the PR cache and DON'T re-warm from DB — re-warming with
	// Persistent entries would defeat the whole purpose of /refresh, since
	// LookupCachedPR would then short-circuit resolvePRsCmd and the user
	// would never pick up newly-opened PRs. A brief flash of empty PR
	// cells while gh runs is acceptable; correctness wins.
	session.ClearPRCache()
	// Reload interactions from DB (picks up changes from other crabctl instances)
	if m.store != nil {
		if li, err := m.store.LoadAllInteractions(); err == nil {
			for k, t := range li {
				if t.After(m.lastInteracted[k]) {
					m.lastInteracted[k] = t
				}
			}
		}
	}
	// Reset SSH failure tracking so failed hosts are retried
	m.remoteFailures = make(map[string]int)
	m.remoteFailed = make(map[string]bool)
	cmds := []tea.Cmd{m.refreshLocalSessions}
	// Mark remote hosts as loading so the spinner shows
	for _, e := range m.executors {
		if e.HostName() != "" {
			m.remoteLoading[e.HostName()] = true
		}
	}
	remoteCmds := m.refreshRemoteSessions()
	cmds = append(cmds, remoteCmds...)
	if len(m.remoteLoading) > 0 {
		cmds = append(cmds, spinnerTickCmd())
	}
	return tea.Batch(cmds...)
}

// resolvePRsCmd dispatches async PR resolution for sessions missing PR URLs.
// Only dispatches for sessions where the cache has no entry (avoids re-resolving
// sessions that are known to have no PR).
func (m Model) resolvePRsCmd() tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range m.sessions {
		if s.PRURL != "" || s.WorkDir == "" {
			continue // already resolved or no workdir
		}
		// Skip if cache already has an entry (even empty = no PR)
		if _, _, _, ok := session.LookupCachedPR(s.Host, s.FullName); ok {
			continue
		}
		host, fullName, workDir := s.Host, s.FullName, s.WorkDir
		exec := m.findExecutor(host)
		cmds = append(cmds, func() tea.Msg {
			pr, prURL, prState := session.ResolveBranchPR(host, fullName, workDir, exec)
			return prResolvedMsg{FullName: fullName, Host: host, PR: pr, PRURL: prURL, PRState: prState}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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
