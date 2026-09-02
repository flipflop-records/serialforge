package tui

import (
	"fmt"
	"time"

	"github.com/vtemnyakov/serialforge/internal/debuglog"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// This file is the Logs tab's data model: a small, bounded, in-memory
// application/session event journal — the third of three intentionally
// separate observability surfaces in SerialForge (see ARCHITECTURE.md
// "Three observability surfaces"):
//
//   - Monitor (m.events, monitor.go)        raw serial TX/RX byte traffic
//   - Logs (m.appLog, this file/logs.go)    user-facing application/session events
//   - SERIALFORGE_DEBUG_LOG (debuglog)      opt-in internal developer trace
//
// Logs must never require SERIALFORGE_DEBUG_LOG to be useful (it's always
// on, by default, for every session) and must never surface raw developer
// trace content — it's a concise, readable event history for a normal
// user, not another Monitor and not a dump of internal routing decisions.

// AppLogLevel is a Logs entry's severity — both its rendered style
// (logLevelTag in logs.go) and, loosely, how noteworthy it is.
type AppLogLevel int

const (
	LogInfo  AppLogLevel = iota // connection/protocol lifecycle, routine
	LogTX                       // a successful send — a distinct color from plain INFO, not a severity
	LogWarn                     // recoverable/expected friction — not connected, disconnected
	LogError                    // an action failed — send failure, protocol missing, connect failure
)

// AppLogEntry is one Logs tab row.
type AppLogEntry struct {
	Time    time.Time
	Level   AppLogLevel
	Message string
}

// maxAppLog bounds the journal — oldest entries are discarded first once
// full. Deliberately smaller than Monitor's maxEventLog (2000): the two
// buffers hold fundamentally different densities of events (every raw
// byte chunk vs. one line per meaningful user/session action), so 1000
// meaningful entries covers a much longer real session than 1000 raw
// packets would.
const maxAppLog = 1000

// logEvent is the ONE append path for the Logs tab's journal — every
// screen/action that wants a user-visible Logs entry calls this, never
// appends to m.appLog directly (mirrors sendTX being the one path for
// Monitor's TX events, and the same reasoning: one choke point makes "no
// duplicate/missing entries" something a single function's tests can
// prove, not something every call site has to individually get right).
//
// Bounded (maxAppLog, oldest dropped first). Also mirrored to the debug
// logger (if enabled) for developers correlating a connect/protocol/send
// action with the resulting journal entry — this does NOT make Logs
// dependent on debug logging: logEvent always appends to m.appLog
// regardless of whether SERIALFORGE_DEBUG_LOG is set; the debuglog.Event
// call is purely an additional, best-effort trace line, never the source
// of truth for what appears in the Logs tab.
func (m *model) logEvent(level AppLogLevel, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	m.appLog = append(m.appLog, AppLogEntry{Time: time.Now(), Level: level, Message: msg})
	if len(m.appLog) > maxAppLog {
		m.appLog = m.appLog[len(m.appLog)-maxAppLog:]
	}
	debuglog.Event("applog", "level", appLogLevelName(level), "message", msg)
}

// sessionStatusLogMessage/sessionStatusLogLevel translate the session
// package's own EventStatus lifecycle (internal/session/session.go:
// StatusDisconnected/Reconnecting/Reconnected/Closed — emitted only by
// the session's automatic reconnect-on-read-error logic, never by a
// user-initiated action) into a Logs entry — this is the ONLY source of
// "disconnected"/"reconnecting"/"reconnected" entries: there is no
// separate user-facing "disconnect" action anywhere in the TUI today (see
// ARCHITECTURE.md), so a manual disconnect never fires this path; only a
// real dropped connection does.
func sessionStatusLogMessage(e session.Event) string {
	msg := e.Status
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func sessionStatusLogLevel(status string) AppLogLevel {
	switch status {
	case session.StatusDisconnected, session.StatusReconnecting:
		return LogWarn
	default: // StatusReconnected, StatusClosed, anything future/unknown
		return LogInfo
	}
}

func appLogLevelName(level AppLogLevel) string {
	switch level {
	case LogTX:
		return "TX"
	case LogWarn:
		return "WARN"
	case LogError:
		return "ERROR"
	default:
		return "INFO"
	}
}
