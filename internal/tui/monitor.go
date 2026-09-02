package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vtemnyakov/serialforge/internal/session"
)

// updateMonitor dispatches to whichever of Monitor's two panes has focus —
// the traffic view (updateMonitorTraffic, the pre-existing pause/clear/mode
// controls, unchanged) or the Saved Packets sidebar (updateMonitorSaved,
// monitorsidebar.go). Falls back to the traffic view whenever the sidebar
// isn't actually on screen (a narrow terminal), so a stale "sidebar
// focused" state left over from a previous wider size can never strand
// Monitor's own keys on an invisible pane.
func (m *model) updateMonitor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.monitorFocus == monitorPaneSaved && m.monitorSidebarVisible() {
		return m.updateMonitorSaved(msg)
	}
	return m.updateMonitorTraffic(msg)
}

// updateMonitorTraffic is Monitor's original key handling: pause/clear,
// cycling display mode. Monitor itself doesn't own connection setup
// (product spec keeps device selection in one place: Devices).
func (m *model) updateMonitorTraffic(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "p":
		m.paused = !m.paused
	case "c":
		m.events = nil
	case "m":
		switch m.monitorMode {
		case "hex":
			m.monitorMode = "ascii"
		case "ascii":
			m.monitorMode = "both"
		default:
			m.monitorMode = "hex"
		}
	}
	return m, nil
}

// viewMonitor renders the Monitor tab: the traffic pane always, and — on a
// wide-enough terminal — the Saved Packets sidebar beside it (see
// monitorsidebar.go). The sidebar is deliberately independent of
// connection state: it must stay usable while disconnected (selecting and
// pressing Enter on a Saved Packet then reports "not connected", the same
// wording sendSavedPacket already produces elsewhere — see task rule on
// disconnected handling), so "not connected" is just the traffic pane's
// own body now, not an early return that would hide the sidebar entirely.
func (m *model) viewMonitor() string {
	// Show only what fits the pane height (bounded render, not bounded
	// history — the full ring buffer is still there for Logs/search later).
	visible := m.height - 10
	if visible < 5 {
		visible = 5
	}

	sidebar := m.monitorSidebarVisible()
	trafficWidth := m.diagramWidth()
	if sidebar {
		// Both panes' rendered width includes boxStyle's own border+padding
		// (monitorBoxOverhead each) — see monitorsidebar.go's doc comment.
		trafficWidth = m.width - m.monitorSidebarWidth() - monitorBoxOverhead - monitorPaneGap - monitorBoxOverhead
		if trafficWidth < monitorTrafficMinWidth {
			trafficWidth = monitorTrafficMinWidth
		}
	}

	var body string
	if m.connectedPath == "" {
		body = dimStyle.Render("Not connected.") + "\n\n" +
			"Go to " + keyStyle.Render("3·Devices") + " to select and connect a serial port,\n" +
			"or run " + keyStyle.Render("serialforge monitor <device>") + " for a headless dump."
	} else {
		var lines []string
		start := 0
		if len(m.events) > visible {
			start = len(m.events) - visible
		}
		for _, e := range m.events[start:] {
			lines = append(lines, formatMonitorLine(e.event, m.monitorMode))
		}
		body = strings.Join(lines, "\n")
		if body == "" {
			body = dimStyle.Render("waiting for data…")
		}
	}

	state := okStyle.Render("live")
	if m.paused {
		state = warnStyle.Render("paused")
	}
	title := fmt.Sprintf("%s   mode=%s   %s", sectionStyle.Render("Monitor"), m.monitorMode, state)

	// Only color the traffic pane's border when there's actually another
	// pane to distinguish it from — a lone full-width pane needs no focus
	// color (task: "avoid excessive borders/colors").
	trafficBorder := normalBorder
	if sidebar && m.monitorFocus == monitorPaneTraffic {
		trafficBorder = selectedBorder
	}
	trafficBox := boxStyle.Width(trafficWidth).Height(visible).BorderForeground(trafficBorder).Render(body)

	var content string
	var hintLine string
	if sidebar {
		content = lipgloss.JoinHorizontal(lipgloss.Top, trafficBox, strings.Repeat(" ", monitorPaneGap), m.viewMonitorSidebar(visible))
		if m.monitorFocus == monitorPaneSaved {
			hintLine = renderHints(hint("tab", "focus traffic"), hint("↑/↓", "select"), hint("enter", "send"),
				hint("←/→", "resize"), hint("r", "reset split"))
		} else {
			hintLine = renderHints(hint("tab", "focus saved packets"), hint("p", "pause/resume"), hint("c", "clear"), hint("m", "cycle hex/ascii/both"))
		}
	} else {
		content = trafficBox
		hintLine = renderHints(hint("p", "pause/resume"), hint("c", "clear"), hint("m", "cycle hex/ascii/both"))
	}

	return title + "\n" + content + "\n" + hintLine
}

func formatMonitorLine(e session.Event, mode string) string {
	ts := dimStyle.Render(e.Timestamp.Format("15:04:05.000"))
	switch e.Kind {
	case session.EventRX:
		return fmt.Sprintf("%s  %s  %s", ts, okStyle.Render("RX"), formatDataByMode(e.Data, mode))
	case session.EventTX:
		return fmt.Sprintf("%s  %s  %s", ts, keyStyle.Render("TX"), formatDataByMode(e.Data, mode))
	case session.EventStatus:
		msg := e.Status
		if e.Err != nil {
			msg += ": " + e.Err.Error()
		}
		return fmt.Sprintf("%s  %s  %s", ts, warnStyle.Render("--"), msg)
	}
	return ""
}

func formatDataByMode(data []byte, mode string) string {
	switch mode {
	case "hex":
		return hexBytes(data)
	case "ascii":
		return asciiOf(data)
	default:
		return hexBytes(data) + "   " + dimStyle.Render(asciiOf(data))
	}
}

func asciiOf(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c < 0x7F {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}
