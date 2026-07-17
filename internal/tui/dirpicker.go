package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/simon/crabctl/internal/session"
	"github.com/simon/crabctl/internal/tmux"
)

type dirPickerStage int

const (
	pickerStageBrowse dirPickerStage = iota
	pickerStageName
)

type dirPickerState struct {
	Stage   dirPickerStage
	Cwd     string   // currently-displayed directory
	AllSubs []string // unfiltered subdir basenames, alphabetical
	Entries []string // post-filter view of AllSubs
	Cursor  int      // index into Entries
	Filter  string   // fuzzy-filter buffer
	Name    string   // session name buffer (stage 2)
	Err     string   // last error to show (e.g. name collision)
	Parent  string   // parent session FullName to nest the new session under (empty = auto-detect)
}

// readSubdirs returns the basenames of immediate subdirectories of cwd,
// excluding dotfile dirs, sorted alphabetically.
func readSubdirs(cwd string) []string {
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return nil
	}
	var subs []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() {
			// Honor symlinks-to-dirs as well
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			info, err := os.Stat(filepath.Join(cwd, name))
			if err != nil || !info.IsDir() {
				continue
			}
		}
		subs = append(subs, name)
	}
	sort.Strings(subs)
	return subs
}

// fuzzyFilter does case-insensitive substring matching. Entries that contain
// the query (as a contiguous substring) are returned in their original order.
func fuzzyFilter(entries []string, query string) []string {
	if query == "" {
		out := make([]string, len(entries))
		copy(out, entries)
		return out
	}
	q := strings.ToLower(query)
	var out []string
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e), q) {
			out = append(out, e)
		}
	}
	return out
}

// uniqueSessionName returns the base name if it's not already taken, otherwise
// "base-2", "base-3", ... until it finds an unused suffix. taken should contain
// names WITHOUT the "crab-" prefix.
func uniqueSessionName(base string, taken map[string]bool) string {
	base = sanitizeSessionName(base)
	if base == "" {
		base = "crab"
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// sanitizeSessionName maps any chars outside [a-zA-Z0-9_-] to '-' and trims
// leading/trailing dashes so the result is a valid crabctl session name.
func sanitizeSessionName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// openDirPicker initializes a picker rooted at startDir (falls back to $HOME
// then "/" if startDir is empty or unreadable).
func openDirPicker(startDir string) *dirPickerState {
	cwd := startDir
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	if cwd == "" {
		cwd = "/"
	}
	// Walk up until we find a readable directory.
	for cwd != "/" {
		if _, err := os.Stat(cwd); err == nil {
			break
		}
		cwd = filepath.Dir(cwd)
	}
	p := &dirPickerState{Cwd: cwd, Stage: pickerStageBrowse}
	p.loadCwd()
	return p
}

// loadCwd refreshes AllSubs/Entries for the current Cwd and resets Cursor.
func (p *dirPickerState) loadCwd() {
	p.AllSubs = readSubdirs(p.Cwd)
	p.Filter = ""
	p.Entries = fuzzyFilter(p.AllSubs, "")
	p.Cursor = 0
}

// applyFilter recomputes Entries from AllSubs and clamps Cursor.
func (p *dirPickerState) applyFilter() {
	p.Entries = fuzzyFilter(p.AllSubs, p.Filter)
	if p.Cursor >= len(p.Entries) {
		p.Cursor = max(0, len(p.Entries)-1)
	}
}

// enterSelected descends into the highlighted subdir if any. No-op if Entries empty.
func (p *dirPickerState) enterSelected() {
	if len(p.Entries) == 0 {
		return
	}
	sub := p.Entries[p.Cursor]
	p.Cwd = filepath.Join(p.Cwd, sub)
	p.loadCwd()
}

// goUp moves to the parent directory and positions the cursor on the dir
// we just left, so ← then → returns to the same place. No-op at /.
func (p *dirPickerState) goUp() {
	parent := filepath.Dir(p.Cwd)
	if parent == p.Cwd {
		return
	}
	leaving := filepath.Base(p.Cwd)
	p.Cwd = parent
	p.loadCwd()
	for i, name := range p.Entries {
		if name == leaving {
			p.Cursor = i
			break
		}
	}
}

// handleDirPickerKey handles keys when the dir-picker overlay is open.
func (m Model) handleDirPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.dirPicker

	if key.Matches(msg, keys.Escape) {
		if p.Stage == pickerStageName {
			p.Stage = pickerStageBrowse
			p.Err = ""
			return m, nil
		}
		m.dirPicker = nil
		return m, nil
	}

	switch p.Stage {
	case pickerStageBrowse:
		return m.handlePickerBrowseKey(msg)
	case pickerStageName:
		return m.handlePickerNameKey(msg)
	}
	return m, nil
}

func (m Model) handlePickerBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.dirPicker

	if key.Matches(msg, keys.Up) {
		if p.Cursor > 0 {
			p.Cursor--
		}
		return m, nil
	}
	if key.Matches(msg, keys.Down) {
		if p.Cursor < len(p.Entries)-1 {
			p.Cursor++
		}
		return m, nil
	}
	if key.Matches(msg, keys.Left) {
		p.goUp()
		return m, nil
	}
	if key.Matches(msg, keys.Right) {
		p.enterSelected()
		return m, nil
	}
	if key.Matches(msg, keys.Enter) {
		// Move to name stage with auto-derived name pre-filled.
		taken := m.takenSessionNames()
		p.Name = uniqueSessionName(filepath.Base(p.Cwd), taken)
		p.Stage = pickerStageName
		p.Err = ""
		return m, nil
	}

	// Editing the filter: backspace + printable runes.
	if msg.Type == tea.KeyBackspace {
		if p.Filter != "" {
			p.Filter = p.Filter[:len(p.Filter)-1]
			p.applyFilter()
		} else {
			// Filter empty → backspace goes up a dir, like a file manager
			p.goUp()
		}
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		// "/" alone resets the filter; otherwise append the runes.
		text := string(msg.Runes)
		if text == "/" && p.Filter == "" {
			// "/" is a no-op marker (filter already lives below); keep empty.
			return m, nil
		}
		p.Filter += text
		p.applyFilter()
		return m, nil
	}
	if msg.Type == tea.KeySpace {
		p.Filter += " "
		p.applyFilter()
		return m, nil
	}

	return m, nil
}

func (m Model) handlePickerNameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.dirPicker

	if key.Matches(msg, keys.Enter) {
		name := sanitizeSessionName(p.Name)
		if name == "" {
			p.Err = "name cannot be empty"
			return m, nil
		}
		fullName := tmux.SessionPrefix + name
		if tmux.HasSession(fullName) {
			p.Err = fmt.Sprintf("session %q already exists", name)
			return m, nil
		}
		dir := p.Cwd
		// Nest the new session under the session that was selected when the
		// picker was opened; fall back to auto-detection when none was set.
		parent := p.Parent
		if parent == "" {
			parent = m.resolveDetectedParent(tmux.DetectParent(""))
		}
		store := m.store
		m.dirPicker = nil
		return m, func() tea.Msg {
			claudeArgs := []string{"--dangerously-skip-permissions"}
			err := tmux.NewSession(name, dir, claudeArgs, parent)
			if err == nil && parent != "" && store != nil {
				store.SaveParent(session.SessionKey("", fullName), parent)
			}
			if err == nil {
				notifyOrchestratorOfSpawn(fullName, dir)
			}
			return sessionCreatedMsg{Name: name, Err: err}
		}
	}

	if msg.Type == tea.KeyBackspace {
		if p.Name != "" {
			p.Name = p.Name[:len(p.Name)-1]
		}
		p.Err = ""
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		p.Name += string(msg.Runes)
		p.Err = ""
		return m, nil
	}
	if msg.Type == tea.KeySpace {
		// Spaces become '-' to keep the name valid.
		p.Name += "-"
		p.Err = ""
		return m, nil
	}
	return m, nil
}

// notifyOrchestratorOfSpawn drops a notification into crab-orchestrator's
// chat input (if it's alive) when a new crab is spawned from the TUI's
// dir-picker. The message is NOT submitted — it lands in the input so the
// orchestrator's user/operator can review/edit/extend it before sending.
//
// No-op if the orchestrator session doesn't exist, or if the new session is
// itself the orchestrator (avoid notifying yourself).
func notifyOrchestratorOfSpawn(spawnedFullName, dir string) {
	orchestrator := tmux.SessionPrefix + OrchestratorName
	if spawnedFullName == orchestrator {
		return
	}
	if !tmux.HasSession(orchestrator) {
		return
	}
	msg := fmt.Sprintf("🔔 New crab session created: %s in %s", spawnedFullName, dir)
	_ = tmux.SendLiteral(orchestrator, msg)
}

// takenSessionNames returns the set of session names (without crab- prefix)
// currently known to the TUI.
func (m Model) takenSessionNames() map[string]bool {
	taken := make(map[string]bool, len(m.sessions))
	for _, s := range m.sessions {
		taken[s.Name] = true
	}
	return taken
}
