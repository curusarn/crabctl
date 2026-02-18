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

	if len(m.sessions) == 0 && m.err == nil {
		b.WriteString("  No sessions. Run: crabctl new <name>\n\n")
	} else if m.err != nil {
		b.WriteString(fmt.Sprintf("  Error: %v\n\n", m.err))
	} else {
		// Table header
		showHost := m.hasRemoteHosts()
		if showHost {
			header := fmt.Sprintf("    %-10s %-32s %-20s %-12s %-8s %-8s %-10s %s",
				"HOST", "NAME", "DIR", "STATUS", "MODE", "ACTIVE", "ATTACHED", "LAST ACTION")
			b.WriteString(headerStyle.Render(header))
		} else {
			header := fmt.Sprintf("    %-32s %-20s %-12s %-8s %-8s %-10s %s",
				"NAME", "DIR", "STATUS", "MODE", "ACTIVE", "ATTACHED", "LAST ACTION")
			b.WriteString(headerStyle.Render(header))
		}
		b.WriteString("\n")

		// Rows (windowed when previewing)
		maxVis := m.maxVisibleSessions()
		end := m.scrollOffset + maxVis
		if end > len(m.filtered) {
			end = len(m.filtered)
		}
		scrollable := len(m.filtered) > maxVis

		// Reserve constant height: when scrollable, always show both indicator lines
		if scrollable {
			if m.scrollOffset > 0 {
				b.WriteString(helpStyle.Render(fmt.Sprintf("    ↑ %d more", m.scrollOffset)))
			}
			b.WriteString("\n")
		}

		for i := m.scrollOffset; i < end; i++ {
			s := m.filtered[i]
			name := s.Name
			if len(name) > 32 {
				name = name[:29] + "..."
			}

			dir := shortenPath(s.WorkDir, 20)
			statusStr := renderStatus(s.Status)
			modeStr := renderMode(s.Mode)
			active := formatActive(s)
			attached := renderAttached(s.AttachedCount)
			action := renderAction(s.LastAction)

			// Build row with manual padding to handle styled strings
			var row string
			if showHost {
				host := s.Host
				if host == "" {
					host = "local"
				}
				row = " " + pad(host, 10) + " " + pad(name, 32) + " " + pad(dir, 20) + " " + pad(statusStr, 12) + " " + pad(modeStr, 8) + " " + pad(active, 8) + " " + pad(attached, 10) + " " + action
			} else {
				row = " " + pad(name, 32) + " " + pad(dir, 20) + " " + pad(statusStr, 12) + " " + pad(modeStr, 8) + " " + pad(active, 8) + " " + pad(attached, 10) + " " + action
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
		m.input.Placeholder = "Type and press Enter to send a message to the session..."
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
	} else if m.preview != nil {
		b.WriteString(helpStyle.Render("enter attach  type+enter send  esc close  j/k navigate  ctrl+k kill"))
	} else if strings.HasPrefix(m.input.Value(), "/new") {
		b.WriteString(helpStyle.Render("/new <name> [dir]  —  create a new session"))
	} else {
		b.WriteString(helpStyle.Render("enter preview  /new <name>  j/k navigate  ctrl+k kill  q quit"))
	}
	b.WriteString("\n")

	return b.String()
}

func renderStatus(s session.Status) string {
	switch s {
	case session.Running:
		return statusRunning.Render("running")
	case session.Waiting:
		return statusWaiting.Render("waiting")
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

func formatActive(s session.Session) string {
	if !s.LastActive.IsZero() {
		return session.FormatDuration(time.Since(s.LastActive))
	}
	return session.FormatDuration(s.Duration)
}

func renderAttached(count int) string {
	if count == 0 {
		return "no"
	}
	if count == 1 {
		return "yes"
	}
	return fmt.Sprintf("yes (%d)", count)
}
