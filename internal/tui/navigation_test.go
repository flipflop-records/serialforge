package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// This file is the regression suite for two related bugs: Tab/Shift+Tab
// being shadowed by Monitor's own pane-focus handling (fixed by moving
// pane focus to "f" and promoting Tab/quit to "hard global controls" —
// see model.handleKey's doc comment), and q/Ctrl+C becoming ineffective
// after certain Monitor state transitions (root-caused via
// SERIALFORGE_DEBUG_LOG-driven reproduction to excessive
// activateProtocol reconnect churn — see this session's final report;
// the fix — a same-protocol no-op in activateProtocol, real
// tea.Cmd propagation, and this explicit priority ordering — removes the
// mechanism, and these tests pin the invariant going forward).

// --- global tab navigation never shadowed by Monitor ------------------------

func TestTabSwitchesTopLevelTabsFromMonitorTrafficFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.monitorFocus = monitorPaneTraffic

	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != tabPackets {
		t.Errorf("Tab from Monitor/Traffic focus = tab %v, want tabPackets", m.tab)
	}
}

func TestTabSwitchesTopLevelTabsFromMonitorSavedFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != tabPackets {
		t.Errorf("Tab from Monitor/Saved focus = tab %v, want tabPackets", m.tab)
	}
	if m.monitorFocus != monitorPaneSaved {
		t.Errorf("Tab must not touch Monitor's own pane focus, got %v", m.monitorFocus)
	}
}

func TestShiftTabSwitchesTopLevelTabsFromEitherMonitorFocus(t *testing.T) {
	for _, pane := range []monitorPane{monitorPaneTraffic, monitorPaneSaved} {
		m := newTestModel(t)
		m = setMonitorWidth(t, m, 120, 40)
		m.monitorFocus = pane

		m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
		if m.tab != tabConfig { // wraps backward from Monitor to the last tab
			t.Errorf("Shift+Tab from pane %v = tab %v, want tabConfig", pane, m.tab)
		}
	}
}

func TestNumericTabShortcutsWorkFromMonitor(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if m.tab != tabDevices {
		t.Errorf("'3' from Monitor = tab %v, want tabDevices", m.tab)
	}
}

// --- Monitor's own pane-focus key is scoped to Monitor only ----------------

func TestMonitorFocusKeyOnlyAffectsMonitor(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.tab = tabDevices
	before := m.monitorFocus

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.monitorFocus != before {
		t.Errorf("f outside Monitor changed monitorFocus: %v -> %v", before, m.monitorFocus)
	}
	if m.tab != tabDevices {
		t.Errorf("f outside Monitor changed the active tab: %v", m.tab)
	}
}

func TestResizeStillWorksAfterFocusSwitch(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")}) // focus the sidebar
	if m.monitorFocus != monitorPaneSaved {
		t.Fatalf("test setup: expected focus on Saved after f, got %v", m.monitorFocus)
	}
	before := m.monitorSidebarWidth()
	m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	after := m.monitorSidebarWidth()
	if after <= before {
		t.Errorf("Left after switching focus via f should still widen the sidebar: before=%d after=%d", before, after)
	}
}

// --- quit is a hard global control, never shadowed --------------------------

func TestQuitFromMonitorTrafficFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.monitorFocus = monitorPaneTraffic

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quit {
		t.Error("q from Monitor/Traffic focus should quit")
	}
}

func TestQuitFromMonitorSavedFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quit {
		t.Error("q from Monitor/Saved focus should quit")
	}
}

func TestQuitAfterSendingAHotkey(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quit {
		t.Error("q after a hotkey send should quit")
	}
}

// TestQuitAfterProtocolActivationReframe specifically exercises the
// scenario the debug-log-driven investigation traced this bug cluster to:
// a genuine protocol switch (different protocol -> reconnect) followed
// immediately by q. Before the fix, activateProtocol reconnected on
// *every* send (even same-protocol), and every reconnect discarded its
// own tea.Cmd — this test pins that q still works right after a real
// reconnect, not just in the steady state.
func TestQuitAfterProtocolActivationReframe(t *testing.T) {
	m := newTestModel(t)
	otherSchema := txTestSchema()
	otherSchema.Name = "control-v2"
	if err := m.cfg.Protocols.Put(otherSchema); err != nil {
		t.Fatal(err)
	}
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Status", "'"))
	v2 := monitorDemoPacket("V2 Ping", ".")
	v2.Protocol = "control-v2"
	_ = m.cfg.SavedPackets.Put(v2)
	received := attachFakeSession(t, m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")}) // activates "demo"
	<-received
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")}) // switches to "control-v2" -> reconnect
	<-received
	if m.activeSchema == nil || m.activeSchema.Name != "control-v2" {
		t.Fatalf("test setup: expected control-v2 active, got %v", m.activeSchema)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quit {
		t.Error("q immediately after a protocol-switching reconnect should quit")
	}
}

func TestQuitAfterDeviceReconnect(t *testing.T) {
	m := newTestModel(t)
	attachFakeSession(t, m) // simulates an initial connect

	// A second connect (e.g. the user reconnected to a different device)
	// via the same path production code uses.
	sc := txTestSchema()
	cmd := m.connect(m.connectedPath, m.connectedCfg, &sc, connectReasonNew)
	if cmd == nil {
		t.Fatal("test setup: expected connect to return a listenSession cmd")
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quit {
		t.Error("q after a device reconnect should quit")
	}
}

func TestCtrlCQuitsFromAllNavigationStates(t *testing.T) {
	states := []struct {
		name  string
		setup func(m *model)
	}{
		{"Monitor/Traffic", func(m *model) { m.tab = tabMonitor; m.monitorFocus = monitorPaneTraffic }},
		{"Monitor/Saved", func(m *model) { m.tab = tabMonitor; m.monitorFocus = monitorPaneSaved }},
		{"Packets", func(m *model) { m.tab = tabPackets }},
		{"Devices", func(m *model) { m.tab = tabDevices }},
		{"Batch", func(m *model) { m.tab = tabBatch }},
		{"Logs", func(m *model) { m.tab = tabLogs }},
		{"Config", func(m *model) { m.tab = tabConfig }},
	}
	for _, s := range states {
		t.Run(s.name, func(t *testing.T) {
			m := newTestModel(t)
			m = setMonitorWidth(t, m, 120, 40)
			s.setup(m)
			m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
			if !m.quit {
				t.Errorf("ctrl+c from %s should quit", s.name)
			}
		})
	}
}

// --- actual text-entry mode still handles text correctly --------------------

// TestTextEntryStillOwnsKeysOverGlobalControls confirms the priority model
// (editors first) is unchanged: a genuinely open text form must still
// receive q/tab as literal characters/navigation for that form, not have
// them stolen by the new "hard global controls" step. Saved Packets'
// rename form is real, on-screen text entry.
func TestTextEntryStillOwnsKeysOverGlobalControls(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}) // open rename form
	if m.saved.mode != savedFormRename || m.saved.form == nil {
		t.Fatal("test setup: rename form should be open")
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if m.quit {
		t.Error("q typed into an open rename form must not quit the application")
	}
	if m.saved.form == nil || !containsRune(m.saved.form.values[0], 'q') {
		t.Errorf("q should have been typed into the rename field, got form=%+v", m.saved.form)
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
