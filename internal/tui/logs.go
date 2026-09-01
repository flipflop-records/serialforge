package tui

import (
	"fmt"
	"strings"

	"github.com/vtemnyakov/serialforge/internal/session"
)

// viewLogs shows connection-lifecycle history (status events: disconnects,
// reconnect attempts, reconnected) — distinct from Monitor's live RX/TX
// byte stream, though both read the same bounded m.events ring buffer
// (application logs are a separate concern from raw capture/packet
// history, even when — as here in v0.1 — they share one in-memory buffer
// rather than separate on-disk files; see ARCHITECTURE.md
// "Known limitations").
func (m *model) viewLogs() string {
	var lines []string
	for _, e := range m.events {
		if e.event.Kind != session.EventStatus {
			continue
		}
		msg := e.event.Status
		if e.event.Err != nil {
			msg += ": " + e.event.Err.Error()
		}
		style := dimStyle
		switch e.event.Status {
		case session.StatusDisconnected:
			style = badStyle
		case session.StatusReconnected:
			style = okStyle
		case session.StatusReconnecting:
			style = warnStyle
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s",
			dimStyle.Render(e.event.Timestamp.Format("15:04:05")), style.Render(e.event.Status), msg))
	}
	if len(lines) == 0 {
		return dimStyle.Render("No connection events yet.")
	}
	if len(lines) > 200 {
		lines = lines[len(lines)-200:]
	}
	return sectionStyle.Render("Connection log") + "\n" + strings.Join(lines, "\n")
}
