package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

// monitorDemoPacket is a Saved Packet referencing the "demo" protocol
// newTestModel registers, with every field filled in (so it resolves and
// builds cleanly) — used across the sidebar tests below.
func monitorDemoPacket(name, hotkey string) savedpacket.SavedPacket {
	return savedpacket.SavedPacket{
		Name:     name,
		Protocol: "demo",
		Values: map[string]string{
			"header":   "AA55",
			"command":  "02",
			"address":  "00C017FF",
			"value":    "FFFF0100",
			"reserved": "0000",
		},
		CRCMode: savedpacket.CRCModeAuto,
		Hotkey:  hotkey,
	}
}

// setMonitorWidth resizes m and puts it on the Monitor tab — the common
// setup every test below starts from.
func setMonitorWidth(t *testing.T, m *model, width, height int) *model {
	t.Helper()
	m.tab = tabMonitor
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(*model)
}

// --- responsive breakpoint ---------------------------------------------------

// 1/2: wide Monitor renders the sidebar; narrow Monitor collapses to
// full-width traffic. The exact breakpoint is derived from
// monitorTrafficMinWidth/monitorSidebarMinWidth/monitorPaneGap/
// monitorBoxOverhead (see monitorsidebar.go), not an arbitrary number.
func TestMonitorSidebarBreakpoint(t *testing.T) {
	need := monitorTrafficMinWidth + monitorBoxOverhead + monitorPaneGap + monitorSidebarMinWidth + monitorBoxOverhead

	m := newTestModel(t)
	m = setMonitorWidth(t, m, need-1, 30)
	if m.monitorSidebarVisible() {
		t.Errorf("width %d (1 below the computed breakpoint %d) should not show the sidebar", need-1, need)
	}
	m = setMonitorWidth(t, m, need, 30)
	if !m.monitorSidebarVisible() {
		t.Errorf("width %d (exactly the computed breakpoint) should show the sidebar", need)
	}
}

func TestViewMonitorRendersSidebarWhenWide(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))

	out := m.viewMonitor()
	if !strings.Contains(out, "Saved Packets") {
		t.Errorf("expected the sidebar to render at width 120, got:\n%s", out)
	}
	if !strings.Contains(out, "Get Status") {
		t.Errorf("expected the saved packet's name in the sidebar, got:\n%s", out)
	}
}

func TestViewMonitorCollapsesToFullWidthWhenNarrow(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 60, 30)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))

	out := m.viewMonitor()
	if strings.Contains(out, "Saved Packets") {
		t.Errorf("sidebar should not render at width 60 (below breakpoint), got:\n%s", out)
	}
	if !strings.Contains(out, "Not connected") {
		t.Errorf("expected the existing full-width Monitor view, got:\n%s", out)
	}
}

// --- protocol filtering -------------------------------------------------------

func TestFilteredSavedPacketsMatchesActiveProtocolOnly(t *testing.T) {
	m := newTestModel(t)
	sc := txTestSchema() // Name: "demo"
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	_ = m.cfg.SavedPackets.Put(savedpacket.SavedPacket{Name: "Other Protocol Packet", Protocol: "control-v2"})

	list := m.filteredSavedPackets()
	if len(list) != 1 || list[0].Name != "Get Status" {
		t.Fatalf("filteredSavedPackets() = %+v, want only \"Get Status\"", list)
	}
}

func TestFilteredSavedPacketsChangesWithActiveProtocol(t *testing.T) {
	m := newTestModel(t)
	demoSchema := txTestSchema()
	otherSchema := txTestSchema()
	otherSchema.Name = "control-v2"
	_ = m.cfg.Protocols.Put(otherSchema)

	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	_ = m.cfg.SavedPackets.Put(savedpacket.SavedPacket{Name: "Reset V2", Protocol: "control-v2"})

	m.activeSchema = &demoSchema
	if got := m.filteredSavedPackets(); len(got) != 1 || got[0].Name != "Get Status" {
		t.Fatalf("with demo active: %+v, want only \"Get Status\"", got)
	}

	m.activeSchema = &otherSchema
	if got := m.filteredSavedPackets(); len(got) != 1 || got[0].Name != "Reset V2" {
		t.Fatalf("with control-v2 active: %+v, want only \"Reset V2\" (no leftover demo packets)", got)
	}
}

func TestFilteredSavedPacketsNoActiveProtocolShowsEmptyState(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.activeSchema = nil
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))

	if got := m.filteredSavedPackets(); got != nil {
		t.Fatalf("filteredSavedPackets() with no active protocol = %+v, want nil", got)
	}
	out := m.viewMonitor()
	if !strings.Contains(out, "No active protocol") {
		t.Errorf("expected the \"No active protocol\" empty state, got:\n%s", out)
	}
}

func TestActiveProtocolWithZeroSavedPacketsShowsEmptyState(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	// No Saved Packets in the store at all.

	out := m.viewMonitor()
	if !strings.Contains(out, "No saved packets for demo") {
		t.Errorf("expected the empty-state wording naming the active protocol, got:\n%s", out)
	}
}

// --- row content ---------------------------------------------------------------

func TestMonitorSidebarRowShowsNameAndHotkey(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Reset", "."))

	out := m.viewMonitor()
	if !strings.Contains(out, "Get Status") || !strings.Contains(out, "'") {
		t.Errorf("expected \"Get Status\" and its hotkey \"'\" in the sidebar, got:\n%s", out)
	}
	if !strings.Contains(out, "Reset") || !strings.Contains(out, ".") {
		t.Errorf("expected \"Reset\" and its hotkey \".\" in the sidebar, got:\n%s", out)
	}
}

// --- selection / navigation ---------------------------------------------------

func TestMonitorSidebarSelectionNavigation(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("A", ""))
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("B", ""))
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("C", ""))
	m.monitorFocus = monitorPaneSaved

	if m.monitorSaved.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.monitorSaved.cursor)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.monitorSaved.cursor != 1 {
		t.Fatalf("after down: cursor = %d, want 1", m.monitorSaved.cursor)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown}) // one more than the list has — must clamp, not go out of range
	if m.monitorSaved.cursor != 2 {
		t.Fatalf("cursor should clamp at the last row: got %d, want 2", m.monitorSaved.cursor)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.monitorSaved.cursor != 1 {
		t.Fatalf("after up: cursor = %d, want 1", m.monitorSaved.cursor)
	}
	if sp, ok := m.monitorSaved.selected(m); !ok || sp.Name != "B" {
		t.Fatalf("selected() = %+v, %v, want \"B\"", sp, ok)
	}
}

// --- direct send ---------------------------------------------------------------

func TestMonitorSidebarEnterSendsSelectedPacket(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	// Blocking receive — the established convention in this test suite
	// (see savedpackets_test.go) for "a send did happen": FakePort's pipe
	// is unbuffered, so a non-blocking select+default here would race the
	// reader goroutine under load instead of actually waiting for it.
	got := <-received
	schema, ok := m.cfg.Protocols.Get("demo")
	if !ok {
		t.Fatal("test setup: \"demo\" protocol not found")
	}
	values := packet.Values{}
	for k, v := range monitorDemoPacket("Get Status", "").Values {
		raw, err := decodeHexTUI(v)
		if err != nil {
			t.Fatal(err)
		}
		values[k] = raw
	}
	wantRaw, _, err := packet.Serialize(schema, values, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wantRaw) {
		t.Errorf("sent bytes = % X, want % X", got, wantRaw)
	}
	if !strings.Contains(m.status, "sent Get Status") {
		t.Errorf("status = %q, want it to mention \"sent Get Status\"", m.status)
	}
}

// TestMonitorSidebarEnterUsesExactSameBytesAsDirectSend proves the sidebar's
// Enter goes through the identical build/send path as the dedicated Saved
// Packets screen's own direct-send ('x') — same SavedPacket, byte-for-byte
// identical output — not a second serializer.
func TestMonitorSidebarEnterUsesExactSameBytesAsDirectSend(t *testing.T) {
	sp := monitorDemoPacket("Get Status", "")

	m1 := newTestModel(t)
	m1 = setMonitorWidth(t, m1, 120, 40)
	sc := txTestSchema()
	m1.activeSchema = &sc
	_ = m1.cfg.SavedPackets.Put(sp)
	rx1 := attachFakeSession(t, m1)
	m1.monitorFocus = monitorPaneSaved
	m1.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	viaSidebar := <-rx1

	m2 := newTestModel(t)
	m2.tab, m2.packetsView = tabPackets, packetsSaved
	_ = m2.cfg.SavedPackets.Put(sp)
	rx2 := attachFakeSession(t, m2)
	m2.saved.cursor = 0
	m2.updateSaved(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // dedicated screen's direct-send
	viaScreen := <-rx2

	if string(viaSidebar) != string(viaScreen) {
		t.Errorf("sidebar bytes = % X, dedicated-screen bytes = % X — must be identical", viaSidebar, viaScreen)
	}
}

// AUTO CRC must be recalculated from current values on every send, not a
// stale cached byte — sending twice after the stored value changes (via the
// store directly, simulating an edit made elsewhere) must reflect the new
// value both times.
func TestMonitorSidebarEnterRecalculatesAutoCRC(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	sp := monitorDemoPacket("Get Status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	first := <-received
	firstCRC := first[len(first)-1]

	sp.Values["command"] = "FF" // changes the packet content the CRC covers
	_ = m.cfg.SavedPackets.Put(sp)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	second := <-received
	secondCRC := second[len(second)-1]

	if firstCRC == secondCRC {
		t.Error("CRC byte did not change after the underlying values changed — AUTO CRC is not being recalculated fresh")
	}
}

func TestMonitorSidebarEnterNotConnectedStatus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	m.monitorFocus = monitorPaneSaved
	// No session attached.

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.status != "Get Status · not connected" {
		t.Errorf("status = %q, want %q", m.status, "Get Status · not connected")
	}
}

func TestMonitorSidebarIncompatiblePacketRefusesToSend(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	broken := savedpacket.SavedPacket{Name: "Broken", Protocol: "demo"} // no Values at all
	_ = m.cfg.SavedPackets.Put(broken)
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case got := <-received:
		t.Fatalf("an incompatible packet must never be sent, got % X", got)
	default:
	}
	if !strings.Contains(m.status, "Broken") {
		t.Errorf("status = %q, want it to name the packet", m.status)
	}

	out := m.viewMonitor()
	if !strings.Contains(out, "!") {
		t.Errorf("the broken packet should be visibly marked in the sidebar, got:\n%s", out)
	}
}

// --- hotkeys remain active regardless of focus ------------------------------

func TestMonitorHotkeyWorksWithTrafficFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneTraffic

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	<-received // blocks until sent — see the same note on the earlier send tests
	if !strings.Contains(m.status, "Get Status") {
		t.Errorf("status = %q, want it to mention Get Status", m.status)
	}
}

func TestMonitorHotkeyWorksWithSidebarFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	<-received // blocks until sent — see the same note on the earlier send tests
	if !strings.Contains(m.status, "'") || !strings.Contains(m.status, "Get Status") {
		t.Errorf("status = %q, want the hotkey-arrow wording naming Get Status", m.status)
	}
}

// --- focus toggling --------------------------------------------------------

// TestTabAlwaysSwitchesTopLevelTabsEvenFromMonitorSidebar is the regression
// test for the Tab-shadowing bug: an earlier version of the adjustable
// split repurposed Tab/Shift+Tab to toggle Monitor's own pane focus
// whenever the sidebar was visible, which silently broke the application's
// global top-level tab navigation the moment a user opened Monitor on a
// wide terminal — see model.handleKey's "hard global controls always win"
// doc comment. Tab/Shift+Tab must cycle top-level tabs unconditionally,
// regardless of Monitor's own focus/sidebar state; "f" is the new,
// Monitor-local pane-focus key (see TestMonitorFocusKeyTogglesPaneFocus).
func TestTabAlwaysSwitchesTopLevelTabsEvenFromMonitorSidebar(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40) // wide: sidebar visible
	m.monitorFocus = monitorPaneSaved  // even with the sidebar focused

	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != tabPackets {
		t.Fatalf("Tab from Monitor (sidebar focused) should cycle to the next top-level tab, got tab=%v", m.tab)
	}
	if m.monitorFocus != monitorPaneSaved {
		t.Errorf("Tab must not touch Monitor's own pane focus, got %v", m.monitorFocus)
	}

	m.tab = tabMonitor
	m.monitorFocus = monitorPaneTraffic
	m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.tab != tabConfig { // wraps backward from Monitor (index 0) to the last tab
		t.Fatalf("Shift+Tab from Monitor should cycle to the previous top-level tab, got tab=%v", m.tab)
	}

	// Narrow: Tab must still cycle top-level tabs (this was already true,
	// confirming no regression there either).
	m.tab = tabMonitor
	m = setMonitorWidth(t, m, 60, 30)
	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != tabPackets {
		t.Fatalf("at a narrow width, tab should cycle to the next top-level tab, got tab=%v", m.tab)
	}
}

// TestMonitorFocusKeyTogglesPaneFocus covers the new Monitor-local
// mechanism ("f") that replaced Tab/Shift+Tab for pane focus.
func TestMonitorFocusKeyTogglesPaneFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40) // wide: sidebar visible
	if m.monitorFocus != monitorPaneTraffic {
		t.Fatalf("test setup: focus should start on traffic")
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.monitorFocus != monitorPaneSaved || m.tab != tabMonitor {
		t.Fatalf("f should switch focus to the sidebar without leaving Monitor, got focus=%v tab=%v", m.monitorFocus, m.tab)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.monitorFocus != monitorPaneTraffic || m.tab != tabMonitor {
		t.Fatalf("f should switch focus back to traffic, got focus=%v tab=%v", m.monitorFocus, m.tab)
	}

	// "f" must not affect any other tab.
	m.tab = tabPackets
	before := m.monitorFocus
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.monitorFocus != before {
		t.Errorf("f pressed outside Monitor changed monitorFocus: %v -> %v", before, m.monitorFocus)
	}

	// Narrow: sidebar not visible, "f" must be inert (there's nothing to
	// focus) rather than silently flipping a pane nobody can see.
	m.tab = tabMonitor
	m = setMonitorWidth(t, m, 60, 30)
	before = m.monitorFocus
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if m.monitorFocus != before {
		t.Errorf("f with the sidebar collapsed changed monitorFocus: %v -> %v", before, m.monitorFocus)
	}
}

func TestMonitorTrafficControlsUnaffectedByFocus(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneTraffic

	before := m.paused
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if m.paused == before {
		t.Error("'p' should still toggle pause when the traffic pane has focus")
	}
}

// --- scrolling / cursor safety ------------------------------------------------

func TestMonitorSidebarScrollingKeepsSelectedRowVisible(t *testing.T) {
	var s monitorSidebarState
	total := 20
	rows := 5

	s.cursor = 12
	start, end := s.visibleWindow(total, rows)
	if s.cursor < start || s.cursor >= end {
		t.Fatalf("cursor %d not within visible window [%d,%d)", s.cursor, start, end)
	}
	if end-start != rows {
		t.Fatalf("window size = %d, want %d", end-start, rows)
	}

	// Jump the cursor back near the top; the window must follow it there too.
	s.cursor = 1
	start, end = s.visibleWindow(total, rows)
	if s.cursor < start || s.cursor >= end {
		t.Fatalf("cursor %d not within visible window [%d,%d) after jumping back", s.cursor, start, end)
	}
}

func TestMonitorSidebarScrollingRenderedListContainsSelected(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 18) // short pane height -> few visible rows
	sc := txTestSchema()
	m.activeSchema = &sc
	for i := 0; i < 15; i++ {
		_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Packet"+strconv.Itoa(i), ""))
	}
	m.monitorFocus = monitorPaneSaved
	for i := 0; i < 14; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.monitorSaved.cursor != 14 {
		t.Fatalf("cursor = %d, want 14", m.monitorSaved.cursor)
	}
	out := m.viewMonitor()
	if !strings.Contains(out, "Packet14") {
		t.Errorf("the selected row (Packet14) must stay visible after scrolling, got:\n%s", out)
	}
}

func TestMonitorSidebarCursorClampsWhenSelectedPacketDeleted(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("A", ""))
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("B", ""))
	m.monitorFocus = monitorPaneSaved
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown}) // select "B" (index 1)
	if m.monitorSaved.cursor != 1 {
		t.Fatalf("test setup: cursor should be 1")
	}

	m.cfg.SavedPackets.Delete("B") // disappears out from under the sidebar

	// Rendering (which clamps) must not panic, and the cursor must land
	// back on something valid.
	out := m.viewMonitor()
	if strings.Contains(out, "panic") {
		t.Fatal("rendering after a deletion panicked")
	}
	if m.monitorSaved.cursor != 0 {
		t.Errorf("cursor after the selected packet disappeared = %d, want 0 (clamped to the new last valid index)", m.monitorSaved.cursor)
	}
	if sp, ok := m.monitorSaved.selected(m); !ok || sp.Name != "A" {
		t.Errorf("selected() after deletion = %+v, %v, want \"A\"", sp, ok)
	}
}

// --- narrow rendering ----------------------------------------------------------

func TestMonitorSidebarNarrowRenderingNoOverflow(t *testing.T) {
	need := monitorTrafficMinWidth + monitorBoxOverhead + monitorPaneGap + monitorSidebarMinWidth + monitorBoxOverhead
	m := newTestModel(t)
	m = setMonitorWidth(t, m, need, 30) // narrowest width the sidebar is still shown at
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("A Reasonably Long Saved Packet Name", "'"))

	out := m.viewMonitor()
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > need {
			t.Errorf("line exceeds the terminal width %d (got %d): %q", need, w, line)
		}
	}
}
