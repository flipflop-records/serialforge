package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vtemnyakov/serialforge/internal/serial"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// This file is the regression suite for the Logs tab: previously scoped to
// only internal/session's own EventStatus (auto-reconnect lifecycle, which
// essentially never fires under normal use — there is no user-facing
// "disconnect" action anywhere in the TUI, and a fresh connect never
// touches it either), so the screen read "No connection events yet." for
// the entire life of a normal session even after substantial real
// activity. Logs is now backed by its own application/session event
// journal (m.appLog, applog.go), populated through one centralized
// m.logEvent call from every meaningful action, distinct from Monitor's
// raw m.events TX/RX buffer and from the opt-in SERIALFORGE_DEBUG_LOG
// developer trace.

func appLogMessages(m *model) []string {
	out := make([]string, len(m.appLog))
	for i, e := range m.appLog {
		out[i] = e.Message
	}
	return out
}

func containsSubstring(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func countContaining(list []string, sub string) int {
	n := 0
	for _, s := range list {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

// --- empty state --------------------------------------------------------

func TestLogsEmptyStateFreshApp(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.tab = tabLogs
	out := m.viewLogs()
	if !strings.Contains(out, "No log entries yet.") {
		t.Errorf("fresh app Logs view = %q, want it to contain \"No log entries yet.\"", out)
	}
	if strings.Contains(out, "No connection events yet.") {
		t.Error("stale empty-state wording still present")
	}
}

// --- connection lifecycle ------------------------------------------------

func TestLogsConnectCreatesEntry(t *testing.T) {
	m := newTestModel(t)
	origOpen := serialOpenFunc
	serialOpenFunc = func(path string, cfg serial.Config) (serial.Port, error) {
		port, dev := serial.NewFakePort()
		t.Cleanup(func() { dev.Close() })
		go func() {
			buf := make([]byte, 256)
			for {
				if _, err := dev.Read(buf); err != nil {
					return
				}
			}
		}()
		return port, nil
	}
	t.Cleanup(func() { serialOpenFunc = origOpen })

	m.connect("/tmp/serialforge-a", serial.Config{Baud: 115200}, nil, connectReasonNew)

	if !containsSubstring(appLogMessages(m), "Connected /tmp/serialforge-a") {
		t.Errorf("appLog = %v, want a \"Connected /tmp/serialforge-a\" entry", appLogMessages(m))
	}
}

func TestLogsDisconnectCreatesEntry(t *testing.T) {
	m := newTestModel(t)
	// There is no user-facing "disconnect" action — the only real source
	// of a disconnected entry is the session's own automatic
	// EventStatus/StatusDisconnected, delivered through the same async
	// path RX arrives through (Update(sessionEventMsg)).
	m.Update(sessionEventMsg(session.Event{Kind: session.EventStatus, Status: session.StatusDisconnected}))

	msgs := appLogMessages(m)
	if !containsSubstring(msgs, "disconnected") {
		t.Errorf("appLog = %v, want a disconnected entry", msgs)
	}
	if len(m.appLog) == 0 || m.appLog[len(m.appLog)-1].Level != LogWarn {
		t.Errorf("disconnected entry should be LogWarn, got %+v", m.appLog)
	}
}

func TestLogsEndpointSwitchCreatesEntries(t *testing.T) {
	m := newTestModel(t)
	origOpen := serialOpenFunc
	serialOpenFunc = func(path string, cfg serial.Config) (serial.Port, error) {
		port, dev := serial.NewFakePort()
		t.Cleanup(func() { dev.Close() })
		go func() {
			buf := make([]byte, 256)
			for {
				if _, err := dev.Read(buf); err != nil {
					return
				}
			}
		}()
		return port, nil
	}
	t.Cleanup(func() { serialOpenFunc = origOpen })

	m.connect("/tmp/serialforge-a", serial.Config{}, nil, connectReasonNew)
	m.connect("/tmp/serialforge-b", serial.Config{}, nil, connectReasonNew)

	msgs := appLogMessages(m)
	if !containsSubstring(msgs, "Connected /tmp/serialforge-a") {
		t.Errorf("missing the first endpoint's Connected entry, appLog = %v", msgs)
	}
	if !containsSubstring(msgs, "Connected /tmp/serialforge-b") {
		t.Errorf("missing the second endpoint's Connected entry, appLog = %v", msgs)
	}
}

// --- protocol context ------------------------------------------------------

func TestLogsProtocolSwitchCreatesOneTransitionEntry(t *testing.T) {
	m := newTestModel(t)
	otherSchema := txTestSchema()
	otherSchema.Name = "control-v2"
	if err := m.cfg.Protocols.Put(otherSchema); err != nil {
		t.Fatal(err)
	}
	demo, _ := m.cfg.Protocols.Get("demo")

	m.activateProtocol(&demo) // first activation, disconnected
	m.activateProtocol(&otherSchema)

	msgs := appLogMessages(m)
	if !containsSubstring(msgs, "Protocol activated: demo") {
		t.Errorf("missing first-activation entry, appLog = %v", msgs)
	}
	if countContaining(msgs, "Protocol switched: demo → control-v2") != 1 {
		t.Errorf("expected exactly one protocol-switch entry, appLog = %v", msgs)
	}
}

func TestLogsProtocolNoOpDoesNotSpam(t *testing.T) {
	m := newTestModel(t)
	demo, _ := m.cfg.Protocols.Get("demo")
	m.activateProtocol(&demo)
	before := len(m.appLog)

	// Repeatedly "activating" the already-active protocol — the common
	// case of pressing the same Saved Packet hotkey over and over.
	for i := 0; i < 5; i++ {
		m.activateProtocol(&demo)
	}

	if got := len(m.appLog); got != before {
		t.Errorf("appLog grew from %d to %d entries after 5 same-protocol activations, want unchanged (no spam)", before, got)
	}
}

// --- TX events ---------------------------------------------------------------

func TestLogsTXBuilderSendCreatesExactlyOneEntry(t *testing.T) {
	m := newTestModel(t)
	sc := txTestSchema()
	m.tab, m.packetsView = tabPackets, packetsTX
	m.tx.schema = &sc
	fillTXValues(&m.tx)
	received := attachFakeSession(t, m)
	m.activeSchema = &sc
	before := len(m.appLog)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	<-received

	added := m.appLog[before:]
	txCount := 0
	for _, e := range added {
		if e.Level == LogTX {
			txCount++
		}
	}
	if txCount != 1 {
		t.Errorf("TX Builder send produced %d LogTX entries, want exactly 1: %v", txCount, appLogMessages(m))
	}
	if !containsSubstring(appLogMessages(m), "TX Builder") {
		t.Errorf("appLog = %v, want a \"TX Builder\" entry", appLogMessages(m))
	}
}

func TestLogsHotkeySendCreatesExactlyOneEntry(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)
	before := len(m.appLog)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received

	txCount := 0
	for _, e := range m.appLog[before:] {
		if e.Level == LogTX {
			txCount++
		}
	}
	if txCount != 1 {
		t.Errorf("hotkey send produced %d LogTX entries, want exactly 1: %v", txCount, appLogMessages(m))
	}
	if !containsSubstring(appLogMessages(m), "Sent Get Status") {
		t.Errorf("appLog = %v, want a \"Sent Get Status\" entry", appLogMessages(m))
	}
}

func TestLogsDirectSendCreatesExactlyOneEntry(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0
	before := len(m.appLog)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	<-received

	txCount := 0
	for _, e := range m.appLog[before:] {
		if e.Level == LogTX {
			txCount++
		}
	}
	if txCount != 1 {
		t.Errorf("direct send produced %d LogTX entries, want exactly 1: %v", txCount, appLogMessages(m))
	}
}

func TestLogsMonitorSidebarEnterCreatesExactlyOneEntry(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved
	before := len(m.appLog)

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	<-received

	txCount := 0
	for _, e := range m.appLog[before:] {
		if e.Level == LogTX {
			txCount++
		}
	}
	if txCount != 1 {
		t.Errorf("Monitor sidebar Enter send produced %d LogTX entries, want exactly 1: %v", txCount, appLogMessages(m))
	}
}

func TestLogsFailedSendCreatesErrorNoSuccess(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	sc, ok := m.cfg.Protocols.Get("demo")
	if !ok {
		t.Fatal("test setup: \"demo\" protocol not found")
	}
	m.activeSchema = &sc
	attachFakeSession(t, m)
	m.activeSchema = &sc
	m.sess.Close() // every Send on it will now fail
	before := len(m.appLog)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	added := m.appLog[before:]
	for _, e := range added {
		if e.Level == LogTX {
			t.Errorf("a failed send must never produce a success (LogTX) entry, got %+v", e)
		}
	}
	if !containsSubstring(appLogMessages(m), "failed") {
		t.Errorf("appLog = %v, want an entry mentioning the failure", appLogMessages(m))
	}
}

// --- bounded history ---------------------------------------------------------

func TestLogsJournalBoundedDropsOldest(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < maxAppLog+50; i++ {
		m.logEvent(LogInfo, "entry %d", i)
	}
	if len(m.appLog) != maxAppLog {
		t.Fatalf("appLog length = %d, want exactly maxAppLog (%d)", len(m.appLog), maxAppLog)
	}
	first := m.appLog[0].Message
	if first != "entry 50" {
		t.Errorf("oldest surviving entry = %q, want \"entry 50\" (the first 50 should have been dropped)", first)
	}
	last := m.appLog[len(m.appLog)-1].Message
	if last != "entry 1049" {
		t.Errorf("newest entry = %q, want \"entry 1049\"", last)
	}
}

// --- scrolling / follow-tail ---------------------------------------------------

func TestLogsScrollingWorks(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 30)
	m.tab = tabLogs
	for i := 0; i < 50; i++ {
		m.logEvent(LogInfo, "entry %d", i)
	}
	rows := logsViewportRows(m.height)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}) // one up from the bottom
	if m.logs.followTail {
		t.Error("scrolling up should disable follow-tail")
	}
	afterOneUp := m.logsEffectiveScroll(rows)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	afterTwoUp := m.logsEffectiveScroll(rows)
	if afterTwoUp != afterOneUp-1 {
		t.Errorf("scrolling up again should move scroll by one more: %d -> %d", afterOneUp, afterTwoUp)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}) // jump to top
	if m.logsEffectiveScroll(rows) != 0 {
		t.Errorf("g should jump to the top, scroll = %d", m.logsEffectiveScroll(rows))
	}
	if m.logs.followTail {
		t.Error("g (top) must not leave follow-tail enabled")
	}
}

func TestLogsFollowTailAtBottom(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 30)
	m.tab = tabLogs
	if !m.logs.followTail {
		t.Fatal("test setup: follow-tail should start enabled")
	}
	for i := 0; i < 3; i++ {
		m.logEvent(LogInfo, "entry %d", i)
	}
	out := m.viewLogs()
	if !strings.Contains(out, "entry 2") {
		t.Errorf("following the tail, the newest entry should be visible, got:\n%s", out)
	}

	// More entries arrive — still following, still shows the newest.
	for i := 3; i < 6; i++ {
		m.logEvent(LogInfo, "entry %d", i)
	}
	out = m.viewLogs()
	if !strings.Contains(out, "entry 5") {
		t.Errorf("after new entries while following, the newest should be visible, got:\n%s", out)
	}
}

func TestLogsScrollingUpDisablesAutoJumpToBottom(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 30) // short height -> few visible rows, easy to scroll away from tail
	m.tab = tabLogs
	for i := 0; i < 30; i++ {
		m.logEvent(LogInfo, "entry %d", i)
	}
	rows := logsViewportRows(m.height)

	// Scroll well away from the bottom.
	for i := 0; i < 10; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	}
	scrollBefore := m.logsEffectiveScroll(rows)
	if m.logs.followTail {
		t.Fatal("test setup: follow-tail should be disabled after scrolling up")
	}

	// A new entry arrives — must NOT yank the view back to the bottom.
	m.logEvent(LogInfo, "entry 30 (new)")
	scrollAfter := m.logsEffectiveScroll(rows)
	if scrollAfter != scrollBefore {
		t.Errorf("a new entry while scrolled up moved the view: %d -> %d, want unchanged", scrollBefore, scrollAfter)
	}
	out := m.viewLogs()
	if strings.Contains(out, "entry 30 (new)") {
		t.Error("the newest entry should not be visible while manually scrolled away from the tail")
	}
}

// --- clear -------------------------------------------------------------------

func TestLogsClearWorks(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 30)
	m.tab = tabLogs
	for i := 0; i < 5; i++ {
		m.logEvent(LogInfo, "entry %d", i)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	if len(m.appLog) != 0 {
		t.Errorf("c should clear the journal, len = %d", len(m.appLog))
	}
	if !m.logs.followTail {
		t.Error("clearing should leave follow-tail enabled")
	}
	out := m.viewLogs()
	if !strings.Contains(out, "No log entries yet.") {
		t.Errorf("after clear, expected the empty state, got:\n%s", out)
	}
}

// --- global keys still work from Logs -----------------------------------------

func TestGlobalKeysWorkFromLogs(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 30)
	m.tab = tabLogs
	for i := 0; i < 5; i++ {
		m.logEvent(LogInfo, "entry %d", i)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != tabConfig { // wraps from Logs (index 4) forward past Config? tabLogs=4, next is tabConfig=5
		t.Errorf("Tab from Logs = tab %v, want tabConfig", m.tab)
	}

	m.tab = tabLogs
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if m.tab != tabMonitor {
		t.Errorf("'1' from Logs = tab %v, want tabMonitor", m.tab)
	}

	m.tab = tabLogs
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quit {
		t.Error("q from Logs should quit")
	}
}

// --- narrow terminal rendering --------------------------------------------------

func TestLogsNarrowTerminalRenderingSane(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 45, 20)
	m.tab = tabLogs
	m.logEvent(LogError, "a fairly long error message that could plausibly overflow a narrow terminal width")
	m.logEvent(LogTX, "Sent A Reasonably Long Saved Packet Name · 14 B")

	out := m.viewLogs()
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 45 {
			t.Errorf("line exceeds terminal width 45 (got %d): %q", w, line)
		}
	}
}
