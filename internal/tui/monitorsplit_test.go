package tui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vtemnyakov/serialforge/internal/config"
)

// This file covers Monitor's adjustable Traffic/Saved-Packets split — see
// monitorsidebar.go's "adjustable split" doc comment for the design this
// exercises: a persisted, normalized ratio (not a column count) that
// resize keys move, that a terminal resize recomputes actual widths from
// but never overwrites, and that a narrow-then-wide-again terminal cycle
// must survive unchanged.

func pressLeft(m *model, n int) {
	for i := 0; i < n; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	}
}

func pressRight(m *model, n int) {
	for i := 0; i < n; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	}
}

func maxLineWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if w := lipgloss.Width(line); w > max {
			max = w
		}
	}
	return max
}

// --- default / basic resize -------------------------------------------------

func TestMonitorSplitDefaultRatioIsSensible(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 140, 40)
	sc := txTestSchema()
	m.activeSchema = &sc

	sidebar := m.monitorSidebarWidth()
	traffic := m.monitorSplitAvailable() - sidebar
	if sidebar < monitorSidebarMinWidth {
		t.Errorf("default sidebar width %d below its own minimum %d", sidebar, monitorSidebarMinWidth)
	}
	if traffic < monitorTrafficMinWidth {
		t.Errorf("default traffic width %d below its own minimum %d", traffic, monitorTrafficMinWidth)
	}
	// The default should keep the traffic pane clearly primary, matching
	// the feature's predecessor's visual balance (~70/30).
	if sidebar >= traffic {
		t.Errorf("default split should keep Traffic the larger pane: sidebar=%d traffic=%d", sidebar, traffic)
	}
}

func TestMonitorSplitUserCanWidenSavedPackets(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 140, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved

	before := m.monitorSidebarWidth()
	pressLeft(m, 3)
	after := m.monitorSidebarWidth()
	if after <= before {
		t.Errorf("Left should widen the sidebar: before=%d after=%d", before, after)
	}
}

func TestMonitorSplitUserCanWidenTraffic(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 140, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved

	before := m.monitorSidebarWidth()
	pressRight(m, 3)
	after := m.monitorSidebarWidth()
	if after >= before {
		t.Errorf("Right should narrow the sidebar (widen traffic): before=%d after=%d", before, after)
	}
}

// --- minimums enforced --------------------------------------------------------

func TestMonitorSplitEnforcesTrafficMinimum(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 140, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved

	pressLeft(m, 500) // far past any reachable boundary
	available := m.monitorSplitAvailable()
	trafficWidth := available - m.monitorSidebarWidth()
	if trafficWidth != monitorTrafficMinWidth {
		t.Errorf("traffic width after maximal widening = %d, want exactly the minimum %d (clamped, not overshot)", trafficWidth, monitorTrafficMinWidth)
	}
	if trafficWidth < monitorTrafficMinWidth {
		t.Fatalf("traffic width %d violates its minimum %d", trafficWidth, monitorTrafficMinWidth)
	}
}

func TestMonitorSplitEnforcesSidebarMinimum(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 140, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved

	pressRight(m, 500)
	sidebarWidth := m.monitorSidebarWidth()
	if sidebarWidth != monitorSidebarMinWidth {
		t.Errorf("sidebar width after maximal narrowing = %d, want exactly the minimum %d (clamped, not overshot)", sidebarWidth, monitorSidebarMinWidth)
	}
}

// A resize request that would violate a minimum must clamp, not corrupt
// state or render a broken layout — repeatedly hammering past the boundary
// must stay stable and keep rendering cleanly.
func TestMonitorSplitResizePastBoundaryStaysStableAndRendersCleanly(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 140, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	m.monitorFocus = monitorPaneSaved

	pressLeft(m, 200)
	out1 := m.viewMonitor()
	pressLeft(m, 50) // already at the boundary — more presses must be harmless
	out2 := m.viewMonitor()
	if out1 != out2 {
		t.Error("pressing resize further past an already-reached boundary changed the rendered output")
	}
	for _, line := range strings.Split(out2, "\n") {
		if w := lipgloss.Width(line); w > 140 {
			t.Errorf("line exceeds terminal width 140 (got %d): %q", w, line)
		}
	}
}

// --- old sidebar cap is gone --------------------------------------------------

func TestMonitorSplitCanExceedOldSidebarCap(t *testing.T) {
	const oldCap = 40 // this feature's predecessor's hard sidebar-width ceiling
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 220, 50)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved

	pressLeft(m, 10)
	if w := m.monitorSidebarWidth(); w <= oldCap {
		t.Errorf("sidebar width after widening = %d, want it able to exceed the old %d-column cap on a wide terminal", w, oldCap)
	}
	// Still bounded by the traffic minimum, not unbounded.
	if traffic := m.monitorSplitAvailable() - m.monitorSidebarWidth(); traffic < monitorTrafficMinWidth {
		t.Errorf("traffic width %d violates its minimum %d even though the old sidebar cap is gone", traffic, monitorTrafficMinWidth)
	}
}

// --- resize preserves everything except layout --------------------------------

func TestMonitorSplitResizePreservesSelectionFocusAndFilter(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("A", ""))
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("B", ""))
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("C", ""))
	m.monitorFocus = monitorPaneSaved
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown}) // select "B"

	beforeList := m.filteredSavedPackets()
	beforeEvents := len(m.events)

	pressLeft(m, 4)
	pressRight(m, 2)

	if sp, ok := m.monitorSaved.selected(m); !ok || sp.Name != "B" {
		t.Errorf("selection after resizing = %+v, %v, want \"B\" unchanged", sp, ok)
	}
	if m.monitorFocus != monitorPaneSaved {
		t.Errorf("focus after resizing = %v, want it to stay on Saved Packets", m.monitorFocus)
	}
	if m.activeSchema.Name != "demo" {
		t.Errorf("active protocol changed after resizing: %q", m.activeSchema.Name)
	}
	afterList := m.filteredSavedPackets()
	if len(afterList) != len(beforeList) {
		t.Errorf("filtered list length changed after resizing: before=%d after=%d", len(beforeList), len(afterList))
	}
	for i := range beforeList {
		if beforeList[i].Name != afterList[i].Name {
			t.Errorf("filtered list order changed after resizing at index %d: %q -> %q", i, beforeList[i].Name, afterList[i].Name)
		}
	}
	if len(m.events) != beforeEvents {
		t.Errorf("resizing must never touch Monitor's traffic events, len changed: before=%d after=%d", beforeEvents, len(m.events))
	}
}

func TestMonitorSplitResizeKeepsSelectedRowVisibleInScrolledList(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 18) // short pane -> few visible rows, forces scrolling
	sc := txTestSchema()
	m.activeSchema = &sc
	for i := 0; i < 15; i++ {
		_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Packet"+string(rune('A'+i)), ""))
	}
	m.monitorFocus = monitorPaneSaved
	for i := 0; i < 14; i++ {
		m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.monitorSaved.cursor != 14 {
		t.Fatalf("test setup: cursor should be 14, got %d", m.monitorSaved.cursor)
	}

	pressLeft(m, 3)
	pressRight(m, 5)

	out := m.viewMonitor()
	if !strings.Contains(out, "PacketO") { // index 14 -> 'A'+14 = 'O'
		t.Errorf("the selected row must stay visible after resizing, got:\n%s", out)
	}
	if m.monitorSaved.cursor != 14 {
		t.Errorf("resizing must never change the selection cursor, got %d", m.monitorSaved.cursor)
	}
}

func TestMonitorSplitResizeDoesNotSendOrChangeFocusOrProtocol(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	pressLeft(m, 5)
	pressRight(m, 3)

	select {
	case got := <-received:
		t.Fatalf("resizing must never send a packet, got % X", got)
	default:
	}
}

// --- send/hotkeys still work after resizing -----------------------------------

func TestMonitorSplitEnterStillSendsAfterResize(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	pressLeft(m, 6)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	<-received // blocks until sent — see the established convention in monitorsidebar_test.go
	if !strings.Contains(m.status, "sent Get Status") {
		t.Errorf("status = %q, want it to mention \"sent Get Status\"", m.status)
	}
}

func TestMonitorSplitHotkeyStillWorksAfterResize(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	pressRight(m, 6)
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	<-received
	if !strings.Contains(m.status, "Get Status") {
		t.Errorf("status = %q, want it to mention Get Status", m.status)
	}
}

// --- collapse / restore across terminal resize --------------------------------

func TestMonitorSplitNarrowTerminalCollapsesSidebar(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved
	pressLeft(m, 4) // custom, non-default preference

	need := monitorTrafficMinWidth + monitorBoxOverhead + monitorPaneGap + monitorSidebarMinWidth + monitorBoxOverhead
	m = setMonitorWidth(t, m, need-1, 40)
	if m.monitorSidebarVisible() {
		t.Fatal("terminal below the breakpoint should collapse the sidebar")
	}
	out := m.viewMonitor()
	if strings.Contains(out, "Saved Packets") {
		t.Errorf("collapsed sidebar must not render, got:\n%s", out)
	}
}

func TestMonitorSplitPreferredRatioSurvivesCollapseAndRestoresOnWiden(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved
	pressLeft(m, 5)
	preferredRatio := m.app.UI.MonitorSavedPacketsRatio
	if preferredRatio == monitorDefaultSavedPacketsRatio {
		t.Fatal("test setup: the preference should have moved off the default")
	}

	// Narrow enough to collapse the sidebar entirely.
	m = setMonitorWidth(t, m, 60, 30)
	if m.monitorSidebarVisible() {
		t.Fatal("test setup: sidebar should be collapsed at width 60")
	}
	if m.app.UI.MonitorSavedPacketsRatio != preferredRatio {
		t.Errorf("collapsing the sidebar must not touch the stored preference: got %v, want %v", m.app.UI.MonitorSavedPacketsRatio, preferredRatio)
	}

	// Widen again — the same preference should govern the restored layout,
	// not the default.
	m = setMonitorWidth(t, m, 160, 40)
	if !m.monitorSidebarVisible() {
		t.Fatal("test setup: sidebar should be visible again at width 160")
	}
	if m.app.UI.MonitorSavedPacketsRatio != preferredRatio {
		t.Errorf("widening again changed the stored preference: got %v, want %v", m.app.UI.MonitorSavedPacketsRatio, preferredRatio)
	}
	if got := m.effectiveMonitorSplitRatio(); got != preferredRatio {
		t.Errorf("effective ratio after widening again = %v, want the restored preference %v", got, preferredRatio)
	}
}

// --- persistence ---------------------------------------------------------------

func TestMonitorSplitDebounceCollapsesBurstIntoOneSave(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved

	pressLeft(m, 3)
	finalGen := m.monitorSaved.saveGen
	if finalGen < 3 {
		t.Fatalf("expected saveGen to have advanced with each keypress, got %d", finalGen)
	}

	appPath := filepath.Join(m.cfg.ConfigDir, "app.yaml")

	// A stale tick left over from an earlier keypress in the burst must not
	// persist anything — only the latest scheduled tick should.
	m.Update(monitorSplitSaveMsg{gen: finalGen - 1})
	if _, err := os.Stat(appPath); err == nil {
		t.Fatal("a stale (superseded) debounce tick must not write app.yaml")
	}

	m.Update(monitorSplitSaveMsg{gen: finalGen})
	data, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("expected app.yaml to exist after the current-generation tick: %v", err)
	}
	if !strings.Contains(string(data), "monitor_saved_packets_ratio") {
		t.Errorf("app.yaml should contain the persisted ratio, got:\n%s", data)
	}
}

func TestMonitorSplitPersistedRatioReloadsAfterRestart(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved
	pressLeft(m, 4)
	wantRatio := m.app.UI.MonitorSavedPacketsRatio

	m.Update(monitorSplitSaveMsg{gen: m.monitorSaved.saveGen}) // simulate the debounce tick firing

	reloaded, err := config.LoadApp(m.cfg.ConfigDir)
	if err != nil {
		t.Fatalf("LoadApp: %v", err)
	}
	if reloaded.UI.MonitorSavedPacketsRatio != wantRatio {
		t.Errorf("reloaded ratio = %v, want %v", reloaded.UI.MonitorSavedPacketsRatio, wantRatio)
	}

	// A brand-new model built from the reloaded config (i.e. "restart")
	// uses the restored preference.
	fresh := newModel(RunConfig{
		ConfigDir:    m.cfg.ConfigDir,
		App:          reloaded,
		Devices:      m.cfg.Devices,
		Protocols:    m.cfg.Protocols,
		SavedPackets: m.cfg.SavedPackets,
		Recent:       m.cfg.Recent,
	})
	fresh = setMonitorWidth(t, fresh, 160, 40)
	if got := fresh.effectiveMonitorSplitRatio(); got != wantRatio {
		t.Errorf("restarted model's effective ratio = %v, want the persisted %v", got, wantRatio)
	}
}

func TestMonitorSplitInvalidPersistedRatioFallsBackSafely(t *testing.T) {
	cases := []float64{0, -0.4, 1, 1.5, math.NaN(), math.Inf(1), math.Inf(-1)}
	for _, r := range cases {
		if got := normalizedMonitorSplitRatio(r); got != monitorDefaultSavedPacketsRatio {
			t.Errorf("normalizedMonitorSplitRatio(%v) = %v, want default %v", r, got, monitorDefaultSavedPacketsRatio)
		}
	}

	// End to end: a model loaded with a malformed stored ratio must still
	// render Monitor cleanly, not collapse or panic.
	m := newTestModel(t)
	m.app.UI.MonitorSavedPacketsRatio = -7
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))

	if got := m.effectiveMonitorSplitRatio(); got != monitorDefaultSavedPacketsRatio {
		t.Errorf("effective ratio with an invalid stored value = %v, want default %v", got, monitorDefaultSavedPacketsRatio)
	}
	out := m.viewMonitor()
	if !strings.Contains(out, "Saved Packets") {
		t.Errorf("an invalid persisted ratio must not prevent the sidebar from rendering, got:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 160 {
			t.Errorf("line exceeds terminal width 160 (got %d): %q", w, line)
		}
	}
}

// --- reset -----------------------------------------------------------------

func TestMonitorSplitResetRestoresDefaultAndPersists(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	m.monitorFocus = monitorPaneSaved
	pressLeft(m, 5)
	if m.app.UI.MonitorSavedPacketsRatio == monitorDefaultSavedPacketsRatio {
		t.Fatal("test setup: ratio should have moved off the default")
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.app.UI.MonitorSavedPacketsRatio != monitorDefaultSavedPacketsRatio {
		t.Errorf("after reset, ratio = %v, want the default %v", m.app.UI.MonitorSavedPacketsRatio, monitorDefaultSavedPacketsRatio)
	}
	if !strings.Contains(strings.ToLower(m.status), "reset") {
		t.Errorf("status after reset = %q, want it to mention the reset", m.status)
	}

	m.Update(monitorSplitSaveMsg{gen: m.monitorSaved.saveGen})
	reloaded, err := config.LoadApp(m.cfg.ConfigDir)
	if err != nil {
		t.Fatalf("LoadApp: %v", err)
	}
	if reloaded.UI.MonitorSavedPacketsRatio != monitorDefaultSavedPacketsRatio {
		t.Errorf("persisted ratio after reset = %v, want the default %v", reloaded.UI.MonitorSavedPacketsRatio, monitorDefaultSavedPacketsRatio)
	}
}

// --- content actually uses the extra width --------------------------------

func TestMonitorSplitLongNameBenefitsFromWidening(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 220, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	longName := "Read Firmware Information And Extended Diagnostics"
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket(longName, "'"))
	m.monitorFocus = monitorPaneSaved

	pressLeft(m, 15)
	if w := m.monitorSidebarWidth(); w <= 40 {
		t.Fatalf("expected a substantially widened sidebar, got %d", w)
	}
	out := m.viewMonitor()
	if !strings.Contains(out, longName) {
		t.Errorf("expected the long packet name to render fully un-truncated at the expanded width, got:\n%s", out)
	}
}

// TestMonitorSplitDiagramReceivesExpandedSidebarWidth drives
// viewMonitorSidebar directly (rather than the full joined-row
// viewMonitor(), whose combined line width is always exactly the terminal
// width regardless of how the two panes divide it) to prove the sidebar's
// own box — and everything inside it, including viewSavedDetail's
// RenderDiagram call — actually receives and uses the wider width, not
// just a wider outer box with hard-coded-width internals.
func TestMonitorSplitDiagramReceivesExpandedSidebarWidth(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 140, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	m.monitorFocus = monitorPaneSaved

	visible := m.height - 10
	narrowSidebarOut := m.viewMonitorSidebar(visible)
	narrowSidebarWidth := m.monitorSidebarWidth()

	pressLeft(m, 12)
	wideSidebarWidth := m.monitorSidebarWidth()
	if wideSidebarWidth <= narrowSidebarWidth {
		t.Fatalf("expected the sidebar to actually widen: narrow=%d wide=%d", narrowSidebarWidth, wideSidebarWidth)
	}
	wideSidebarOut := m.viewMonitorSidebar(visible)

	if maxLineWidth(wideSidebarOut) <= maxLineWidth(narrowSidebarOut) {
		t.Errorf("expected the sidebar box itself (list + detail + diagram) to render wider: narrow max line=%d, wide max line=%d", maxLineWidth(narrowSidebarOut), maxLineWidth(wideSidebarOut))
	}
	// And it should track monitorSidebarWidth()'s own numbers, not some
	// unrelated internal constant.
	if got := maxLineWidth(wideSidebarOut); got != wideSidebarWidth+monitorBoxOverhead {
		t.Errorf("wide sidebar box footprint = %d, want monitorSidebarWidth()+overhead = %d", got, wideSidebarWidth+monitorBoxOverhead)
	}
}

// --- key hint --------------------------------------------------------------

func TestMonitorSplitKeyHintReflectsChosenKeysOnlyWhenSavedFocusedAndVisible(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))

	m.monitorFocus = monitorPaneSaved
	focused := m.viewMonitor()
	if !strings.Contains(focused, "←/→") || !strings.Contains(focused, "resize") {
		t.Errorf("expected the resize hint (←/→ resize) when Saved Packets has focus, got:\n%s", focused)
	}
	if !strings.Contains(focused, "reset split") {
		t.Errorf("expected a reset-split hint when Saved Packets has focus, got:\n%s", focused)
	}

	m.monitorFocus = monitorPaneTraffic
	unfocused := m.viewMonitor()
	if strings.Contains(unfocused, "resize") {
		t.Errorf("resize hint must not show when the traffic pane has focus, got:\n%s", unfocused)
	}

	// Collapsed sidebar: no resize hint even though the focus field still
	// says Saved Packets (defensive — mirrors the existing Tab fallback).
	m.monitorFocus = monitorPaneSaved
	narrow := setMonitorWidth(t, m, 60, 30)
	collapsed := narrow.viewMonitor()
	if strings.Contains(collapsed, "resize") {
		t.Errorf("collapsed sidebar must not show a resize hint, got:\n%s", collapsed)
	}
}

// --- hotkey/palette safety ----------------------------------------------------

// TestPaletteKeysNeverConsumedByMonitorSidebarDispatch is the mechanical
// check behind this feature's "left"/"right"/"r" choice (see
// updateMonitorSaved's doc comment): keybindings.go's
// TestPaletteKeysAreNeverConsumedByCoreDispatch exercises the Monitor
// scenario at its default (zero) width, where the sidebar isn't visible and
// updateMonitorSaved is never actually reached — this drives the same
// palette against the sidebar-focused dispatch specifically, with the
// sidebar visible and a Saved Packet present, so a future accidental
// resize/reset binding from the palette would fail here even though it
// would slip past the width-zero scenario.
func TestPaletteKeysNeverConsumedByMonitorSidebarDispatch(t *testing.T) {
	for _, key := range hotkeyPalette {
		t.Run(key, func(t *testing.T) {
			m := newTestModel(t)
			m = setMonitorWidth(t, m, 160, 40)
			sc := txTestSchema()
			m.activeSchema = &sc
			_ = m.cfg.SavedPackets.Put(monitorDemoPacket("A", "")) // no hotkey assigned
			m.monitorFocus = monitorPaneSaved

			before := fmt.Sprintf("cursor=%d ratio=%v gen=%d", m.monitorSaved.cursor, m.app.UI.MonitorSavedPacketsRatio, m.monitorSaved.saveGen)
			m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			after := fmt.Sprintf("cursor=%d ratio=%v gen=%d", m.monitorSaved.cursor, m.app.UI.MonitorSavedPacketsRatio, m.monitorSaved.saveGen)
			if before != after {
				t.Errorf("palette key %q changed sidebar selection/resize state: before=%s after=%s — palette keys must never be consumed by Monitor's sidebar dispatch", key, before, after)
			}
		})
	}
}

// --- boundary widths ---------------------------------------------------------

func TestMonitorSplitNoOverflowAtBoundaryWidths(t *testing.T) {
	need := monitorTrafficMinWidth + monitorBoxOverhead + monitorPaneGap + monitorSidebarMinWidth + monitorBoxOverhead
	widths := []int{need, need + 1, 100, 160, 400}

	for _, width := range widths {
		m := newTestModel(t)
		m = setMonitorWidth(t, m, width, 30)
		sc := txTestSchema()
		m.activeSchema = &sc
		_ = m.cfg.SavedPackets.Put(monitorDemoPacket("A Reasonably Long Saved Packet Name", "'"))
		m.monitorFocus = monitorPaneSaved

		// Push to both extremes at this exact width.
		pressLeft(m, 300)
		if out := m.viewMonitor(); true {
			for _, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("width=%d, maximally widened sidebar: line exceeds terminal width (got %d): %q", width, w, line)
				}
			}
		}
		pressRight(m, 600)
		if out := m.viewMonitor(); true {
			for _, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > width {
					t.Errorf("width=%d, maximally narrowed sidebar: line exceeds terminal width (got %d): %q", width, w, line)
				}
			}
		}
	}
}
