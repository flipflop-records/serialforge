package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the Logs tab's screen — scrolling/rendering over the
// journal m.appLog owns (see applog.go for the data model and why Logs is
// deliberately not the same buffer Monitor reads).

// logsState is Logs' own UI state — a scroll position and a follow-tail
// flag, the standard log-viewer "tail -f" idiom: while followTail is true,
// the view always shows the newest entries and new ones keep it pinned to
// the bottom; the moment the user scrolls up, followTail turns off so a
// new event arriving mid-read doesn't yank the view away, and it turns
// back on once the user scrolls (or jumps) back to the bottom. scroll only
// matters while followTail is false — logsEffectiveScroll recomputes the
// true render position from followTail on every call, so nothing needs to
// proactively keep scroll in sync when new entries arrive.
type logsState struct {
	scroll     int
	followTail bool
}

func newLogsState() logsState { return logsState{followTail: true} }

// logsViewportRows mirrors Monitor's own traffic-pane height budget
// (viewMonitor) so the two scrollable panes in this app feel consistent
// rather than each screen inventing its own sizing rule.
func logsViewportRows(height int) int {
	rows := height - 10
	if rows < 5 {
		rows = 5
	}
	return rows
}

// logsEffectiveScroll is the actual top-of-viewport index to render from,
// clamped to what m.appLog's current length actually allows — computed
// fresh every call (not cached) so a change in terminal height or the
// journal's own length (new entries, or a clear) is always reflected
// correctly without needing an explicit "resync" step.
func (m *model) logsEffectiveScroll(rows int) int {
	maxScroll := len(m.appLog) - rows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.logs.followTail {
		return maxScroll
	}
	return clampInt(m.logs.scroll, 0, maxScroll)
}

// updateLogs: scroll (↑/↓/j/k), jump to top/bottom (g/G — see
// keybindings.go's palette; both already unused elsewhere so this claims
// no new reservation), and c to clear the journal. Chosen only after
// checking the centralized keybinding policy — none of these collide with
// global navigation or the Saved Packet hotkey palette.
func (m *model) updateLogs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.logs
	rows := logsViewportRows(m.height)
	cur := m.logsEffectiveScroll(rows)
	maxScroll := len(m.appLog) - rows
	if maxScroll < 0 {
		maxScroll = 0
	}
	switch msg.String() {
	case "up", "k":
		s.scroll = clampInt(cur-1, 0, maxScroll)
		s.followTail = false
	case "down", "j":
		next := clampInt(cur+1, 0, maxScroll)
		s.scroll = next
		s.followTail = next >= maxScroll
	case "g":
		s.scroll = 0
		s.followTail = false
	case "G":
		s.scroll = maxScroll
		s.followTail = true
	case "c":
		m.appLog = nil
		s.scroll = 0
		s.followTail = true
	}
	return m, nil
}

func (m *model) viewLogs() string {
	rows := logsViewportRows(m.height)
	width := m.diagramWidth()

	var body string
	newBelow := 0
	if len(m.appLog) == 0 {
		body = secondaryStyle.Render("No log entries yet.")
	} else {
		scroll := m.logsEffectiveScroll(rows)
		end := scroll + rows
		if end > len(m.appLog) {
			end = len(m.appLog)
		}
		lines := make([]string, 0, end-scroll)
		for _, e := range m.appLog[scroll:end] {
			lines = append(lines, formatAppLogLine(e))
		}
		body = strings.Join(lines, "\n")
		if !m.logs.followTail {
			newBelow = len(m.appLog) - end
		}
	}

	title := sectionStyle.Render("Logs")
	if newBelow > 0 {
		title += "  " + warnStyle.Render(fmt.Sprintf("+%d new", newBelow))
	}
	box := boxStyle.Width(width).Height(rows).Render(body)
	hintLine := renderHints(hint("↑/↓", "scroll"), hint("g/G", "top/bottom"), hint("c", "clear"))
	return title + "\n" + box + "\n" + hintLine
}

func formatAppLogLine(e AppLogEntry) string {
	ts := dimStyle.Render(e.Time.Format("15:04:05.000"))
	return fmt.Sprintf("%s  %s  %s", ts, logLevelTag(e.Level), e.Message)
}

// logLevelTag styles just the level label — the message itself stays
// unstyled/plain (matching Monitor's own formatMonitorLine convention for
// its EventStatus line: the kind tag carries the color, the content
// doesn't), so Logs reads as calm, readable history rather than a loud
// rainbow of colored text (task requirement).
func logLevelTag(level AppLogLevel) string {
	switch level {
	case LogTX:
		return keyStyle.Render("TX")
	case LogWarn:
		return warnStyle.Render("WARN")
	case LogError:
		return badStyle.Render("ERROR")
	default:
		return primaryStyle.Render("INFO")
	}
}
