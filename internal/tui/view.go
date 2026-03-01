package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/simon/crabctl/internal/session"
	"github.com/simon/crabctl/internal/tmux"
)

var (
	// Adaptive colors for light/dark terminal backgrounds
	accentColor = lipgloss.AdaptiveColor{Light: "#D6249F", Dark: "#FF79C6"}
	greenColor  = lipgloss.AdaptiveColor{Light: "#116620", Dark: "#50FA7B"}
	yellowColor = lipgloss.AdaptiveColor{Light: "#7D5A00", Dark: "#F1FA8C"}
	redColor    = lipgloss.AdaptiveColor{Light: "#B31D28", Dark: "#FF5555"}
	dimColor    = lipgloss.AdaptiveColor{Light: "#777777", Dark: "#6272A4"}
	hlBgColor   = lipgloss.AdaptiveColor{Light: "#E8E8E8", Dark: "#333333"}
	cyanColor   = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#8BE9FD"}

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			PaddingLeft(1)

	headerStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			PaddingLeft(1)

	cursorStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	selectedRowStyle = lipgloss.NewStyle().
				Background(hlBgColor)

	statusRunning = lipgloss.NewStyle().
			Foreground(greenColor)

	statusWaiting = lipgloss.NewStyle().
			Foreground(yellowColor)

	statusPermission = lipgloss.NewStyle().
				Foreground(redColor).
				Bold(true)

	statusUnknown = lipgloss.NewStyle().
			Foreground(dimColor)

	modeStyle = lipgloss.NewStyle().
			Foreground(cyanColor)

	actionStyle = lipgloss.NewStyle().
			Foreground(dimColor)

	confirmLabelStyle = lipgloss.NewStyle().
				Foreground(redColor).
				Bold(true).
				PaddingLeft(1)

	confirmKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#FFFFFF"}).
			Background(redColor).
			Bold(true).
			Padding(0, 1)

	confirmDimStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			PaddingLeft(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(dimColor).
			PaddingLeft(1)

	inputLabelStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	previewBorderStyle = lipgloss.NewStyle().
				Foreground(dimColor)

	previewContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#444444", Dark: "#BBBBBB"})
)

// highlightRow applies the highlight background to an entire row,
// persisting through inner ANSI style resets. It works by extracting
// the raw background escape sequence from lipgloss and re-injecting
// it after every SGR reset (\x1b[0m) in the row.
func highlightRow(row string) string {
	// Render a probe character to extract the background escape sequence
	probe := selectedRowStyle.Render("X")
	xIdx := strings.Index(probe, "X")
	if xIdx <= 0 {
		return selectedRowStyle.Render(row)
	}
	bgOpen := probe[:xIdx]

	// Apply background at the start and re-apply after every SGR reset
	result := bgOpen + strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m"+bgOpen) + "\x1b[0m"
	return result
}

// pad right-pads s to width with spaces (based on visual width, not byte count).
func pad(s string, width int) string {
	visual := lipgloss.Width(s)
	if visual >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visual)
}

// shortenPath abbreviates a path for display (replaces $HOME with ~, truncates).
func shortenPath(path string, maxLen int) string {
	if path == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		path = "~" + path[len(home):]
	}
	if len(path) <= maxLen {
		return path
	}
	return "…" + path[len(path)-(maxLen-1):]
}

func (m Model) View() string {
	if m.quitting && m.AttachTarget == "" {
		return ""
	}

	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("crabctl"))
	b.WriteString("\n\n")

	if m.resumeMode {
		m.renderResumeList(&b, m.preview != nil)
	} else if len(m.sessions) == 0 && m.err == nil {
		b.WriteString("  No sessions. Run: crabctl new <name>\n")
		if !m.hasRemoteHosts() && os.Getenv("INFRASTRUCTURE_AS_RUBY_PATH") != "" {
			b.WriteString(helpStyle.Render("  Set WORKBENCH_HOST to manage remote sessions") + "\n")
		}
		b.WriteString("\n")
	} else if m.err != nil {
		b.WriteString(fmt.Sprintf("  Error: %v\n\n", m.err))
	} else {
		showHost := m.hasRemoteHosts()

		// Rows (windowed when previewing)
		maxVis := m.maxVisibleSessions()
		end := m.scrollOffset + maxVis
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		scrollable := len(m.filtered) > maxVis

		// Precompute cell values for visible rows
		type rowData struct {
			host, name, dir, status, mode, info, changes string
			treePrefix string // rendered tree prefix (dim styled)
			virtual    bool   // virtual parent placeholder
		}
		rows := make([]rowData, 0, end-m.scrollOffset)
		for i := m.scrollOffset; i < end; i++ {
			s := m.filtered[i]
			name := s.Name
			// When hiding children, show child count on parents
			if s.HiddenCount > 0 {
				name += fmt.Sprintf(" (%d)", s.HiddenCount)
			}
			if len(name) > 32 {
				name = name[:29] + "..."
			}
			host := s.Host
			if host == "" && showHost && !s.Virtual {
				host = "local"
			}
			mode := s.Mode
			if m.autoForward[s.FullName] {
				mode = "autoforward"
			}
			rd := rowData{
				host:       host,
				name:       name,
				dir:        shortenPath(s.WorkDir, 20),
				status:     renderStatusWithAge(s),
				mode:       renderMode(mode),
				info:       renderInfo(s),
				changes:    renderChanges(s),
				treePrefix: s.TreePrefix,
				virtual:    s.Virtual,
			}
			if s.Virtual {
				rd.status = statusUnknown.Render("-")
				rd.mode = statusUnknown.Render("-")
				rd.info = ""
				rd.changes = ""
				rd.dir = ""
			}
			rows = append(rows, rd)
		}

		// Measure column widths (using lipgloss.Width for ANSI-aware measurement)
		type colSpec struct {
			min, max, width int
			header          string
		}
		cols := []colSpec{
			{min: 4, max: 40, header: "NAME"},
			{min: 4, max: 20, header: "DIR"},
			{min: 7, max: 14, header: "STATUS"},
			{min: 4, max: 12, header: "MODE"},
			{min: 4, max: 40, header: "INFO"},
		}
		hostCol := colSpec{min: 4, max: 10, header: "HOST"}

		// Measure from data (include tree prefix in NAME width)
		for _, r := range rows {
			nameWidth := len(r.treePrefix) + lipgloss.Width(r.name)
			if nameWidth > cols[0].width {
				cols[0].width = nameWidth
			}
			vals := []string{"", r.dir, r.status, r.mode, r.info}
			for j := 1; j < len(vals); j++ {
				w := lipgloss.Width(vals[j])
				if w > cols[j].width {
					cols[j].width = w
				}
			}
			if showHost {
				w := lipgloss.Width(r.host)
				if w > hostCol.width {
					hostCol.width = w
				}
			}
		}
		// Also measure headers, then clamp
		for j := range cols {
			hw := len(cols[j].header)
			if hw > cols[j].width {
				cols[j].width = hw
			}
			if cols[j].width < cols[j].min {
				cols[j].width = cols[j].min
			}
			if cols[j].width > cols[j].max {
				cols[j].width = cols[j].max
			}
		}
		if showHost {
			hw := len(hostCol.header)
			if hw > hostCol.width {
				hostCol.width = hw
			}
			if hostCol.width < hostCol.min {
				hostCol.width = hostCol.min
			}
			if hostCol.width > hostCol.max {
				hostCol.width = hostCol.max
			}
		}

		// Measure CHANGES column width (last column, needs padding for even highlights)
		wChanges := len("CHANGES")
		for _, r := range rows {
			w := lipgloss.Width(r.changes)
			if w > wChanges {
				wChanges = w
			}
		}

		wName, wDir, wStatus, wMode, wInfo := cols[0].width, cols[1].width, cols[2].width, cols[3].width, cols[4].width

		// Render header
		if showHost {
			header := "  " + pad("HOST", hostCol.width) + "  " + pad("NAME", wName) + "  " + pad("DIR", wDir) + "  " + pad("STATUS", wStatus) + "  " + pad("MODE", wMode) + "  " + pad("INFO", wInfo) + "  " + pad("CHANGES", wChanges)
			b.WriteString(headerStyle.Render(header))
		} else {
			header := "  " + pad("NAME", wName) + "  " + pad("DIR", wDir) + "  " + pad("STATUS", wStatus) + "  " + pad("MODE", wMode) + "  " + pad("INFO", wInfo) + "  " + pad("CHANGES", wChanges)
			b.WriteString(headerStyle.Render(header))
		}
		b.WriteString("\n")

		// Reserve constant height: when scrollable, always show both indicator lines
		if scrollable {
			if m.scrollOffset > 0 {
				b.WriteString(helpStyle.Render(fmt.Sprintf("    ↑ %d more", m.scrollOffset)))
			}
			b.WriteString("\n")
		}

		// Render rows
		for ri, r := range rows {
			i := m.scrollOffset + ri
			s := m.filtered[i]
			// Build name cell with tree prefix
			nameCell := r.name
			if r.treePrefix != "" {
				nameCell = actionStyle.Render(r.treePrefix) + r.name
			}

			var row string
			if showHost {
				row = " " + pad(r.host, hostCol.width) + "  " + pad(nameCell, wName) + "  " + pad(r.dir, wDir) + "  " + pad(r.status, wStatus) + "  " + pad(r.mode, wMode) + "  " + pad(r.info, wInfo) + "  " + pad(r.changes, wChanges)
			} else {
				row = " " + pad(nameCell, wName) + "  " + pad(r.dir, wDir) + "  " + pad(r.status, wStatus) + "  " + pad(r.mode, wMode) + "  " + pad(r.info, wInfo) + "  " + pad(r.changes, wChanges)
			}

			// Virtual (no tmux session) parents are fully dimmed
			if r.virtual {
				row = statusUnknown.Render(ansi.Strip(row))
			}

			isCursor := i == m.cursor
			isSelected := m.selected[s.FullName]
			switch {
			case isSelected && isCursor:
				b.WriteString(cursorStyle.Render("◆>"))
				b.WriteString(highlightRow(row))
			case isSelected:
				b.WriteString(cursorStyle.Render("◆ "))
				b.WriteString(row)
			case isCursor:
				b.WriteString(cursorStyle.Render(" >"))
				b.WriteString(highlightRow(row))
			default:
				b.WriteString("  ")
				b.WriteString(row)
			}
			b.WriteString("\n")
		}

		if scrollable {
			if end < len(m.filtered) {
				b.WriteString(helpStyle.Render(fmt.Sprintf("    ↓ %d more", len(m.filtered)-end)))
			}
			b.WriteString("\n")
		}

		// Loading indicator for remote hosts
		if len(m.remoteLoading) > 0 {
			spinnerChars := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
			spinner := string(spinnerChars[m.spinnerFrame%len(spinnerChars)])
			var hosts []string
			for h := range m.remoteLoading {
				hosts = append(hosts, h)
			}
			sort.Strings(hosts)
			b.WriteString(helpStyle.Render(fmt.Sprintf("    %s fetching %s...", spinner, strings.Join(hosts, ", "))))
			b.WriteString("\n")
		}

		// Failed SSH hosts
		if len(m.remoteFailed) > 0 {
			var failedHosts []string
			for h := range m.remoteFailed {
				failedHosts = append(failedHosts, h)
			}
			sort.Strings(failedHosts)
			b.WriteString(statusPermission.Render("    ⚠︎ ssh failed " + strings.Join(failedHosts, ", ")))
			b.WriteString(helpStyle.Render(" · /refresh to try again"))
			b.WriteString("\n")
		}

		// Hint for users with infrastructure-as-ruby but no workbench configured
		if !showHost && os.Getenv("INFRASTRUCTURE_AS_RUBY_PATH") != "" {
			b.WriteString(helpStyle.Render("    Set WORKBENCH_HOST to manage remote sessions"))
			b.WriteString("\n")
		}

		b.WriteString("\n")
	}

	// Preview panel (height-limited to keep session list visible)
	if m.resumeMode && m.preview != nil {
		borderTitle := fmt.Sprintf(" ─── %s ", m.preview.SessionName)
		titleWidth := lipgloss.Width(borderTitle)
		remaining := m.width - titleWidth - 2
		if remaining > 0 {
			borderTitle += strings.Repeat("─", remaining)
		}
		b.WriteString(previewBorderStyle.Render(" " + borderTitle))
		b.WriteString("\n")

		if m.preview.Output != "" {
			previewLines := strings.Split(m.preview.Output, "\n")

			// Budget for resume mode: title+blank(2) + header(1) + "Resume..."(1) + gap(1) + visible rows + borders(2) + input(1) + help(1) + safety(1)
			maxVis := m.maxVisibleResumeSessions()
			overhead := 10 + maxVis
			maxPreview := m.height - overhead
			if maxPreview < 3 {
				maxPreview = 3
			}

			start := len(previewLines) - maxPreview
			if start < 0 {
				start = 0
			}
			for _, line := range previewLines[start:] {
				b.WriteString(previewContentStyle.Render(" " + line))
				b.WriteString("\n")
			}
		} else {
			b.WriteString(previewContentStyle.Render(" Loading..."))
			b.WriteString("\n")
		}

		borderBottom := strings.Repeat("─", max(0, m.width-2))
		b.WriteString(previewBorderStyle.Render(" " + borderBottom))
		b.WriteString("\n")
	} else if m.preview != nil {
		borderTitle := fmt.Sprintf(" ─── %s ", m.preview.SessionName)
		titleWidth := lipgloss.Width(borderTitle)
		remaining := m.width - titleWidth - 2
		if remaining > 0 {
			borderTitle += strings.Repeat("─", remaining)
		}
		b.WriteString(previewBorderStyle.Render(" " + borderTitle))
		b.WriteString("\n")

		if m.preview.Output != "" {
			previewLines := strings.Split(m.preview.Output, "\n")

			// Budget: title+blank(2) + header(1) + visible sessions + scroll indicators(0 or 2) + loading(0-1) + gap(1) + borders(2) + input(1) + help(1) + safety(1)
			visibleRows := m.maxVisibleSessions()
			scrollIndicators := 0
			if len(m.filtered) > visibleRows {
				scrollIndicators = 2 // always reserve both lines when scrollable
			}
			loadingLine := 0
			if len(m.remoteLoading) > 0 {
				loadingLine = 1
			}
			overhead := 9 + visibleRows + scrollIndicators + loadingLine
			maxPreview := m.height - overhead
			if maxPreview < 3 {
				maxPreview = 3
			}

			// Show the last N lines (most recent output)
			start := len(previewLines) - maxPreview
			if start < 0 {
				start = 0
			}
			for _, line := range previewLines[start:] {
				b.WriteString(previewContentStyle.Render(" " + line))
				b.WriteString("\n")
			}
		} else {
			b.WriteString(previewContentStyle.Render(" Loading..."))
			b.WriteString("\n")
		}

		borderBottom := strings.Repeat("─", max(0, m.width-2))
		b.WriteString(previewBorderStyle.Render(" " + borderBottom))
		b.WriteString("\n")
	}

	// Input line (placeholder changes based on mode)
	if m.resumeMode && m.preview != nil {
		m.input.Placeholder = "Press enter to resume this session..."
	} else if m.preview != nil {
		m.input.Placeholder = "Type and press enter to send a message to the session..."
	} else {
		m.input.Placeholder = "Type to filter, /command, ? for shortcuts"
	}
	b.WriteString(inputLabelStyle.Render(" > "))
	b.WriteString(m.input.View())
	// Ghost text: show selected suggestion completion inline
	if val := strings.TrimSpace(m.input.Value()); strings.HasPrefix(val, "/") && !strings.Contains(val, " ") && m.preview == nil && !m.resumeMode {
		matches := matchingCommands(val)
		idx := m.suggestCursor
		if idx >= len(matches) {
			idx = 0
		}
		if len(matches) > 0 {
			ghost := strings.TrimPrefix(matches[idx].Name, val)
			if ghost != "" {
				b.WriteString(helpStyle.Render(ghost))
			}
		}
	}
	b.WriteString("\n")

	// Help bar / kill confirmation (same slot to avoid layout shift)
	if m.confirmKill != nil && m.confirmKill.Killing {
		spinnerChars := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		spinner := string(spinnerChars[m.spinnerFrame%len(spinnerChars)])
		killLabel := m.confirmKillLabel()
		b.WriteString(confirmLabelStyle.Render(fmt.Sprintf("%s Killing %s...", spinner, killLabel)))
	} else if m.confirmKill != nil {
		killLabel := m.confirmKillLabel()
		b.WriteString(confirmLabelStyle.Render(fmt.Sprintf("Kill %s?", killLabel)))
		b.WriteString("  ")
		b.WriteString(confirmKeyStyle.Render("Enter"))
		b.WriteString(confirmDimStyle.Render("confirm"))
		b.WriteString("  ")
		b.WriteString(confirmKeyStyle.Render("Esc"))
		b.WriteString(confirmDimStyle.Render("cancel"))
	} else if m.resumeMode && m.preview != nil {
		b.WriteString(helpStyle.Render("enter resume  ↑/↓ navigate  esc close preview"))
	} else if m.resumeMode {
		b.WriteString(helpStyle.Render("enter preview  type to filter  ↑/↓ navigate  esc back"))
	} else if m.preview != nil {
		b.WriteString(helpStyle.Render("enter attach  type+enter send  esc close  ↑/↓ navigate  ctrl+a autoforward  ctrl+k kill"))
	} else if matches := matchingCommands(m.input.Value()); strings.HasPrefix(strings.TrimSpace(m.input.Value()), "/") && len(matches) > 0 {
		highlightStyle := lipgloss.NewStyle().Foreground(accentColor).PaddingLeft(1)
		idx := m.suggestCursor
		if idx >= len(matches) {
			idx = 0
		}
		for i, c := range matches {
			line := fmt.Sprintf("  %-20s — %s", c.Usage, c.Description)
			if i == idx {
				b.WriteString(highlightStyle.Render(line))
			} else {
				b.WriteString(helpStyle.Render(line))
			}
			b.WriteString("\n")
		}
	} else if strings.TrimSpace(m.input.Value()) == "?" {
		b.WriteString(helpStyle.Render("enter preview  ↑/↓ navigate  space select  ctrl+r refresh  ctrl+a autoforward  ctrl+h fold  ctrl+k kill"))
	} else {
		b.WriteString(helpStyle.Render("enter preview  ↑/↓ navigate  space select  ctrl+k kill"))
	}
	b.WriteString("\n")

	return b.String()
}

// confirmKillLabel returns a human-readable label for the kill confirmation.
func (m Model) confirmKillLabel() string {
	if m.confirmKill == nil || len(m.confirmKill.Targets) == 0 {
		return ""
	}
	if len(m.confirmKill.Targets) == 1 {
		return fmt.Sprintf("'%s'", m.confirmKill.Targets[0].Name)
	}
	return fmt.Sprintf("%d sessions", len(m.confirmKill.Targets))
}

func (m Model) renderResumeList(b *strings.Builder, showPreview bool) {
	b.WriteString(headerStyle.Render("  Resume a session"))
	b.WriteString("\n\n")

	if len(m.resumeFiltered) == 0 {
		b.WriteString("  No matching sessions found.\n\n")
		return
	}

	header := fmt.Sprintf("    %-8s %-16s %-24s %s", "AGO", "NAME", "PROJECT", "MESSAGE")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	maxVis := m.maxVisibleResumeSessions()
	start := 0
	if m.resumeCursor >= maxVis {
		start = m.resumeCursor - maxVis + 1
	}
	end := start + maxVis
	if end > len(m.resumeFiltered) {
		end = len(m.resumeFiltered)
	}

	for i := start; i < end; i++ {
		cs := m.resumeFiltered[i]
		age := session.FormatDuration(time.Since(cs.ModTime))
		if !cs.Killed {
			age += " ~"
		}
		name := strings.TrimPrefix(cs.Name, tmux.SessionPrefix)
		if len(name) > 16 {
			name = name[:13] + "..."
		}
		project := shortenPath(cs.ProjectDir, 24)
		msg := cs.FirstMessage
		if len(msg) > 40 {
			msg = msg[:37] + "..."
		}

		row := " " + pad(age, 8) + " " + pad(name, 16) + " " + pad(project, 24) + " " + actionStyle.Render(msg)

		if i == m.resumeCursor {
			b.WriteString(cursorStyle.Render(" >"))
			b.WriteString(selectedRowStyle.Render(row))
		} else {
			b.WriteString("  ")
			b.WriteString(row)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// maxVisibleResumeSessions returns the max rows to show in the resume list.
// When preview is open, limit to fewer rows to leave room for the preview panel.
func (m Model) maxVisibleResumeSessions() int {
	if m.preview != nil {
		maxVis := m.height / 10
		if maxVis < 5 {
			maxVis = 5
		}
		if maxVis > len(m.resumeFiltered) {
			maxVis = len(m.resumeFiltered)
		}
		return maxVis
	}
	maxVis := 20
	if m.height > 0 {
		maxVis = m.height - 10
		if maxVis < 5 {
			maxVis = 5
		}
	}
	return maxVis
}

func renderStatusWithAge(s session.Session) string {
	switch s.Status {
	case session.Running:
		return statusRunning.Render("running")
	case session.Waiting:
		label := statusWaiting.Render("waiting")
		if !s.LastActive.IsZero() {
			label += " " + actionStyle.Render(session.FormatDurationCoarse(time.Since(s.LastActive)))
		}
		return label
	case session.Permission:
		return statusPermission.Render("permission")
	case session.Confirm:
		return statusPermission.Render("confirm")
	case session.TaskDone:
		return statusPermission.Render("task done")
	default:
		return statusUnknown.Render("unknown")
	}
}

func renderMode(mode string) string {
	if mode == "" {
		return statusUnknown.Render("-")
	}
	if mode == "autoforward" {
		return statusPermission.Render("autoforward")
	}
	return modeStyle.Render(mode)
}

func renderAction(action string) string {
	if action == "" {
		return ""
	}
	return actionStyle.Render(action)
}

func renderInfo(s session.Session) string {
	var parts []string

	if s.LastAction != "" {
		parts = append(parts, actionStyle.Render(s.LastAction))
	}
	if s.Context != "" {
		parts = append(parts, statusPermission.Render("ctx:"+s.Context))
	}

	return strings.Join(parts, actionStyle.Render(" · "))
}

func renderChanges(s session.Session) string {
	var parts []string

	if s.GitChanges != "" {
		parts = append(parts, actionStyle.Render(s.GitChanges))
	}
	if s.PR != "" {
		pr := modeStyle.Render(s.PR)
		if s.PRURL != "" {
			pr = ansi.SetHyperlink(s.PRURL) + pr + ansi.ResetHyperlink()
		}
		parts = append(parts, pr)
	}

	return strings.Join(parts, actionStyle.Render(" · "))
}

