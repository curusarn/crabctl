package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/simon/crabctl/internal/session"
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
		m.renderResumeList(&b)
	} else if len(m.sessions) == 0 && m.err == nil {
		b.WriteString("  No sessions. Run: crabctl new <name>\n\n")
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
		}
		rows := make([]rowData, 0, end-m.scrollOffset)
		for i := m.scrollOffset; i < end; i++ {
			s := m.filtered[i]
			name := s.Name
			if len(name) > 32 {
				name = name[:29] + "..."
			}
			host := s.Host
			if host == "" && showHost {
				host = "local"
			}
			rows = append(rows, rowData{
				host:    host,
				name:    name,
				dir:     shortenPath(s.WorkDir, 20),
				status:  renderStatusWithAge(s),
				mode:    renderMode(s.Mode),
				info:    renderInfo(s),
				changes: renderChanges(s),
			})
		}

		// Measure column widths (using lipgloss.Width for ANSI-aware measurement)
		type colSpec struct {
			min, max, width int
			header          string
		}
		cols := []colSpec{
			{min: 4, max: 32, header: "NAME"},
			{min: 4, max: 20, header: "DIR"},
			{min: 7, max: 14, header: "STATUS"},
			{min: 4, max: 8, header: "MODE"},
			{min: 4, max: 40, header: "INFO"},
		}
		hostCol := colSpec{min: 4, max: 10, header: "HOST"}

		// Measure from data
		for _, r := range rows {
			vals := []string{r.name, r.dir, r.status, r.mode, r.info}
			for j, v := range vals {
				w := lipgloss.Width(v)
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

		wName, wDir, wStatus, wMode, wInfo := cols[0].width, cols[1].width, cols[2].width, cols[3].width, cols[4].width

		// Render header
		if showHost {
			header := "    " + pad("HOST", hostCol.width) + "  " + pad("NAME", wName) + "  " + pad("DIR", wDir) + "  " + pad("STATUS", wStatus) + "  " + pad("MODE", wMode) + "  " + pad("INFO", wInfo) + "  CHANGES"
			b.WriteString(headerStyle.Render(header))
		} else {
			header := "    " + pad("NAME", wName) + "  " + pad("DIR", wDir) + "  " + pad("STATUS", wStatus) + "  " + pad("MODE", wMode) + "  " + pad("INFO", wInfo) + "  CHANGES"
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
			var row string
			if showHost {
				row = " " + pad(r.host, hostCol.width) + "  " + pad(r.name, wName) + "  " + pad(r.dir, wDir) + "  " + pad(r.status, wStatus) + "  " + pad(r.mode, wMode) + "  " + pad(r.info, wInfo) + "  " + r.changes
			} else {
				row = " " + pad(r.name, wName) + "  " + pad(r.dir, wDir) + "  " + pad(r.status, wStatus) + "  " + pad(r.mode, wMode) + "  " + pad(r.info, wInfo) + "  " + r.changes
			}

			if i == m.cursor {
				b.WriteString(cursorStyle.Render(" >"))
				b.WriteString(selectedRowStyle.Render(row))
			} else {
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

		b.WriteString("\n")
	}

	// Preview panel (height-limited to keep session list visible)
	if m.preview != nil {
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
	if m.preview != nil {
		m.input.Placeholder = "Type and press enter to send a message to the session..."
	} else {
		m.input.Placeholder = "Type to filter or enter command..."
	}
	b.WriteString(inputLabelStyle.Render(" > "))
	b.WriteString(m.input.View())
	b.WriteString("\n")

	// Help bar / kill confirmation (same slot to avoid layout shift)
	if m.confirmKill != nil {
		b.WriteString(confirmLabelStyle.Render(fmt.Sprintf("Kill '%s'?", m.confirmKill.SessionName)))
		b.WriteString("  ")
		b.WriteString(confirmKeyStyle.Render("Enter"))
		b.WriteString(confirmDimStyle.Render("confirm"))
		b.WriteString("  ")
		b.WriteString(confirmKeyStyle.Render("Esc"))
		b.WriteString(confirmDimStyle.Render("cancel"))
	} else if m.resumeMode {
		b.WriteString(helpStyle.Render("enter resume  type to filter  j/k navigate  esc back"))
	} else if m.preview != nil {
		b.WriteString(helpStyle.Render("enter attach  type+enter send  esc close  j/k navigate  ctrl+k kill"))
	} else if strings.HasPrefix(m.input.Value(), "/new") {
		b.WriteString(helpStyle.Render("/new <name> [dir]  —  create a new session"))
	} else if strings.HasPrefix(m.input.Value(), "/resume") {
		b.WriteString(helpStyle.Render("/resume  —  browse and resume past Claude sessions"))
	} else {
		b.WriteString(helpStyle.Render("enter preview  /new  /resume  j/k navigate  ctrl+k kill  q quit"))
	}
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderResumeList(b *strings.Builder) {
	b.WriteString(headerStyle.Render("  Resume a past Claude session"))
	b.WriteString("\n\n")

	if len(m.resumeFiltered) == 0 {
		b.WriteString("  No matching sessions found.\n\n")
		return
	}

	header := fmt.Sprintf("    %-8s %-30s %s", "AGE", "PROJECT", "MESSAGE")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	// Show up to 20 visible sessions
	maxVis := 20
	if m.height > 0 {
		maxVis = m.height - 10
		if maxVis < 5 {
			maxVis = 5
		}
	}
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
		project := shortenPath(cs.ProjectDir, 30)
		msg := cs.FirstMessage
		if len(msg) > 50 {
			msg = msg[:47] + "..."
		}

		row := " " + pad(age, 8) + " " + pad(project, 30) + " " + actionStyle.Render(msg)

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
	default:
		return statusUnknown.Render("unknown")
	}
}

func renderMode(mode string) string {
	if mode == "" {
		return statusUnknown.Render("-")
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
		parts = append(parts, modeStyle.Render(s.PR))
	}

	return strings.Join(parts, actionStyle.Render(" · "))
}

