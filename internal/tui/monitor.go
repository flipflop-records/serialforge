package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/session"
)

// updateMonitor handles keys while the Monitor tab is active: pause/clear,
// cycling display mode, and (if not connected) a hint to use the Devices
// tab — Monitor itself doesn't own connection setup (product spec keeps
// device selection in one place: Devices).
func (m *model) updateMonitor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m *model) viewMonitor() string {
	if m.connectedPath == "" {
		return boxStyle.Width(m.diagramWidth()).Render(
			dimStyle.Render("Not connected.") + "\n\n" +
				"Go to " + keyStyle.Render("3·Devices") + " to select and connect a serial port,\n" +
				"or run " + keyStyle.Render("serialforge monitor <device>") + " for a headless dump.")
	}

	var lines []string
	start := 0
	// Show only what fits the pane height (bounded render, not bounded
	// history — the full ring buffer is still there for Logs/search later).
	visible := m.height - 10
	if visible < 5 {
		visible = 5
	}
	if len(m.events) > visible {
		start = len(m.events) - visible
	}
	for _, e := range m.events[start:] {
		lines = append(lines, formatMonitorLine(e.event, m.monitorMode))
	}
	body := strings.Join(lines, "\n")
	if body == "" {
		body = dimStyle.Render("waiting for data…")
	}

	state := okStyle.Render("live")
	if m.paused {
		state = warnStyle.Render("paused")
	}
	title := fmt.Sprintf("%s   mode=%s   %s", sectionStyle.Render("Monitor"), m.monitorMode, state)
	hintLine := renderHints(hint("p", "pause/resume"), hint("c", "clear"), hint("m", "cycle hex/ascii/both"))

	return title + "\n" + boxStyle.Width(m.diagramWidth()).Height(visible).Render(body) + "\n" + hintLine
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
