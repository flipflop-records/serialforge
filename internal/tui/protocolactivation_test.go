package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
	"github.com/vtemnyakov/serialforge/internal/serial"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// This file is the regression suite for the bug where sending a Saved
// Packet via its hotkey (or the dedicated Saved screen's direct-send) built
// and transmitted a packet for its referenced Protocol without ever making
// that Protocol the TUI's active context — so the Monitor sidebar (which
// filters strictly off m.activeSchema) stayed empty or showed the wrong
// list until the user separately visited Packets -> Saved -> Enter, the
// one path (loadSavedPacketIntoTX) that already activated it correctly.
// The fix centralizes activation in model.activateProtocol and calls it
// from sendSavedPacket — the one shared send path both hotkey and direct
// send already funnel through — see savedpackets.go.

// waitTUIEvent is waitEvent (internal/session/session_test.go) inlined
// locally — internal/tui can't import a test-only helper from another
// package's _test.go file.
func waitTUIEvent(t *testing.T, events <-chan session.Event, kind session.EventKind, timeout time.Duration) session.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				t.Fatalf("events channel closed while waiting for %s", kind)
			}
			if e.Kind == kind {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event kind %s", kind)
		}
	}
}

// --- 1: the exact reported bug, driven through the real key path -----------

// TestHotkeySendPopulatesMonitorSidebarWithoutVisitingSavedScreen is the
// literal original repro: fresh model, no active protocol, no visit to
// Packets -> Saved at all — press the hotkey, then render Monitor, and the
// sidebar must already show the right packets.
func TestHotkeySendPopulatesMonitorSidebarWithoutVisitingSavedScreen(t *testing.T) {
	m := newTestModel(t)
	if m.activeSchema != nil {
		t.Fatal("test setup: model should start with no active protocol")
	}
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'")) // Protocol: "demo"
	received := attachFakeSession(t, m)

	// Never touch m.saved / packetsSaved / TX Builder — go straight to the
	// hotkey, exactly like the bug report's reproduction steps.
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received // blocks until sent — established convention, see monitorsidebar_test.go

	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Fatalf("activeSchema after hotkey send = %v, want \"demo\"", m.activeSchema)
	}

	m = setMonitorWidth(t, m, 120, 40)
	out := m.viewMonitor()
	if !strings.Contains(out, "Saved Packets") {
		t.Fatalf("expected the sidebar to render at all, got:\n%s", out)
	}
	if !strings.Contains(out, "Get Status") {
		t.Errorf("expected \"Get Status\" in the sidebar immediately after the hotkey send, with no visit to Packets -> Saved, got:\n%s", out)
	}
	if list := m.filteredSavedPackets(); len(list) != 1 || list[0].Name != "Get Status" {
		t.Errorf("filteredSavedPackets() = %+v, want only \"Get Status\"", list)
	}
}

// --- 2/3: direct Saved-screen send and TX-Builder load share the fix -------

func TestDirectSavedSendActivatesProtocol(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)

	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // dedicated screen's direct-send
	<-received

	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Fatalf("activeSchema after Saved screen direct-send = %v, want \"demo\"", m.activeSchema)
	}
}

// TestLoadIntoTXBuilderStillActivatesProtocol pins the pre-existing correct
// behavior (the fix reuses/extracts this, never duplicates it — see
// model.activateProtocol and its call site in loadSavedPacketIntoTX).
func TestLoadIntoTXBuilderStillActivatesProtocol(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))

	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}) // load into TX Builder

	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Fatalf("activeSchema after loading into TX Builder = %v, want \"demo\"", m.activeSchema)
	}
	if m.packetsView != packetsTX {
		t.Errorf("expected loading to switch to TX Builder, packetsView = %v", m.packetsView)
	}
}

// --- 4/7/8: switching between Saved Packets for different protocols --------

func TestHotkeySwitchesActiveProtocolAcrossDifferentSavedPackets(t *testing.T) {
	m := newTestModel(t)
	otherSchema := txTestSchema()
	otherSchema.Name = "control-v2"
	if err := m.cfg.Protocols.Put(otherSchema); err != nil {
		t.Fatal(err)
	}
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Status", "'")) // Protocol: "demo"
	v2 := monitorDemoPacket("V2 Ping", ".")
	v2.Protocol = "control-v2"
	_ = m.cfg.SavedPackets.Put(v2)
	received := attachFakeSession(t, m)
	m = setMonitorWidth(t, m, 120, 40)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received
	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Fatalf("after ': activeSchema = %v, want \"demo\"", m.activeSchema)
	}
	out := m.viewMonitor()
	if !strings.Contains(out, "Status") || strings.Contains(out, "V2 Ping") {
		t.Errorf("sidebar after ' should show only demo's \"Status\", not control-v2's \"V2 Ping\", got:\n%s", out)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")})
	<-received
	if m.activeSchema == nil || m.activeSchema.Name != "control-v2" {
		t.Fatalf("after .: activeSchema = %v, want \"control-v2\"", m.activeSchema)
	}
	out = m.viewMonitor()
	if !strings.Contains(out, "V2 Ping") || strings.Contains(out, "Status") {
		t.Errorf("sidebar after . should immediately switch to only control-v2's \"V2 Ping\", got:\n%s", out)
	}
}

// --- 9: disconnected invocation still establishes protocol context ---------

func TestHotkeyDisconnectedStillActivatesProtocolButReportsNotConnected(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	// No session attached at all.

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Errorf("activeSchema while disconnected = %v, want \"demo\" (context should still establish)", m.activeSchema)
	}
	if m.status != "Get Status · not connected" {
		t.Errorf("status = %q, want %q", m.status, "Get Status · not connected")
	}
}

// --- 10/11: broken Saved Packets must never corrupt active protocol --------

func TestHotkeyMissingProtocolDoesNotCorruptActiveProtocol(t *testing.T) {
	m := newTestModel(t)
	sc := txTestSchema()
	m.activeSchema = &sc // a known-good protocol already active
	ghost := savedpacket.SavedPacket{Name: "Ghost", Protocol: "does-not-exist", Hotkey: "'"}
	_ = m.cfg.SavedPackets.Put(ghost)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Errorf("activeSchema after a missing-protocol hotkey = %v, want it unchanged (\"demo\")", m.activeSchema)
	}
	if !strings.Contains(m.status, "Ghost") {
		t.Errorf("status = %q, want it to name the broken packet", m.status)
	}
}

// TestHotkeyIncompatiblePacketActivatesItsValidProtocolButRefusesToSend
// pins the deliberate design choice: StatusIncompatible means the
// Protocol itself resolved fine (only this packet's stored values are
// stale — see savedpacket.Resolution's doc comment, which is why
// loadSavedPacketIntoTX already loads an Incompatible packet instead of
// refusing), so activation follows that same StatusOK-or-StatusIncompatible
// rule sendSavedPacket now uses — the packet still doesn't send, and the
// resulting state is fully coherent (a real, resolved schema), never a
// half-updated mix.
func TestHotkeyIncompatiblePacketActivatesItsValidProtocolButRefusesToSend(t *testing.T) {
	m := newTestModel(t)
	broken := savedpacket.SavedPacket{Name: "Broken", Protocol: "demo", Hotkey: "'"} // no Values at all -> incompatible
	_ = m.cfg.SavedPackets.Put(broken)
	received := attachFakeSession(t, m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	select {
	case got := <-received:
		t.Fatalf("an incompatible packet must never be sent, got % X", got)
	default:
	}
	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Errorf("activeSchema after an incompatible-but-valid-protocol hotkey = %v, want \"demo\" (the protocol itself is real)", m.activeSchema)
	}
	if !strings.Contains(m.status, "Broken") {
		t.Errorf("status = %q, want it to name the packet", m.status)
	}
}

// --- 12: byte-exact regression — activation must not change what's sent ----

func TestHotkeySendsExactSameBytesAsBeforeThisFix(t *testing.T) {
	sp := monitorDemoPacket("Get Status", "'")

	m1 := newTestModel(t) // starts with NO active protocol — exercises the fixed path
	_ = m1.cfg.SavedPackets.Put(sp)
	rx1 := attachFakeSession(t, m1)
	m1.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	viaHotkeyNoPriorContext := <-rx1

	m2 := newTestModel(t)
	sc := txTestSchema()
	m2.activeSchema = &sc // already active — the path that worked before this fix too
	_ = m2.cfg.SavedPackets.Put(sp)
	rx2 := attachFakeSession(t, m2)
	m2.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	viaHotkeyAlreadyActive := <-rx2

	if string(viaHotkeyNoPriorContext) != string(viaHotkeyAlreadyActive) {
		t.Errorf("bytes differ depending on whether the protocol was already active: %X vs %X — activation must not change what's built/sent", viaHotkeyNoPriorContext, viaHotkeyAlreadyActive)
	}
}

// --- 13: RX framing must not stay stale on the old protocol ----------------

// TestHotkeyActivationReframesLiveSession connects with one (small,
// 4-byte) protocol's framing active, then triggers a hotkey for a Saved
// Packet referencing a different (14-byte) protocol, and proves — via
// actually injecting bytes on the wire and watching how the session frames
// them — that the LIVE session's RX framing switched too, not just the
// visible m.activeSchema pointer. This is the deepest check in this file:
// model.connect() builds framing.Framer fresh from whatever schema it's
// given, so if activateProtocol only swapped the pointer instead of
// reconnecting, this test would see 4-byte frames both before and after.
func TestHotkeyActivationReframesLiveSession(t *testing.T) {
	m := newTestModel(t)
	small := packet.Schema{
		Name:      "small",
		TotalSize: 4,
		Fields:    []packet.Field{{Name: "x", Size: 4, Format: packet.FormatHex}},
		Checksum:  checksum.Definition{Mode: checksum.ModeNone},
	}
	if err := m.cfg.Protocols.Put(small); err != nil {
		t.Fatal(err)
	}
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'")) // Protocol: "demo", TotalSize 14

	var mu sync.Mutex
	var curDev *serial.FakeDevice
	origOpen := serialOpenFunc
	serialOpenFunc = func(path string, cfg serial.Config) (serial.Port, error) {
		port, dev := serial.NewFakePort()
		mu.Lock()
		curDev = dev
		mu.Unlock()
		t.Cleanup(func() { dev.Close() })
		go func() { // drain the TX side so the hotkey's own Send() never blocks
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
	getDev := func() *serial.FakeDevice {
		mu.Lock()
		defer mu.Unlock()
		return curDev
	}

	sc := small
	m.connect("/fake", serial.Config{}, &sc, connectReasonNew)
	if m.sess == nil || m.activeSchema == nil || m.activeSchema.Name != "small" {
		t.Fatalf("test setup: expected connect to succeed with \"small\" active, activeSchema=%v sess-nil=%v", m.activeSchema, m.sess == nil)
	}

	// Sanity: 4-byte framing really is in effect before the hotkey.
	go func() { _, _ = getDev().Write([]byte{0x01, 0x02, 0x03, 0x04}) }()
	e := waitTUIEvent(t, m.sess.Events(), session.EventRX, 2*time.Second)
	if len(e.Data) != 4 {
		t.Fatalf("test setup: framed RX = %d bytes, want 4 (\"small\"'s TotalSize)", len(e.Data))
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")}) // activates "demo" (14 bytes)
	if m.activeSchema == nil || m.activeSchema.Name != "demo" {
		t.Fatalf("activeSchema after hotkey = %v, want \"demo\"", m.activeSchema)
	}

	go func() { _, _ = getDev().Write(make([]byte, 14)) }()
	e = waitTUIEvent(t, m.sess.Events(), session.EventRX, 2*time.Second)
	if len(e.Data) != 14 {
		t.Errorf("after the hotkey activated \"demo\", framed RX event = %d bytes, want 14 — the live session's framing is still stale from \"small\"", len(e.Data))
	}
}

// TestHotkeyActivationReconnectsRatherThanJustSwappingPointer is a
// cheaper, more robust companion to the framing test above: proves a
// genuine reconnect happened (a new *session.Session, which connect()
// always builds with a fresh framer from the given schema), not merely an
// in-place mutation of the old session's state.
func TestHotkeyActivationReconnectsRatherThanJustSwappingPointer(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)
	oldSess := m.sess

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received

	if m.sess == nil {
		t.Fatal("session should still be connected after the hotkey")
	}
	if m.sess == oldSess {
		t.Error("expected activateProtocol to reconnect (a new *session.Session with a schema-appropriate framer), got the exact same session instance")
	}
}

// --- 14: adjustable split preference/focus untouched by protocol switch ----

func TestHotkeyProtocolSwitchDoesNotDisturbMonitorSplitState(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 160, 40)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)

	m.monitorFocus = monitorPaneSaved
	m.app.UI.MonitorSavedPacketsRatio = 0.5
	m.monitorSaved.cursor = 0

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received

	if m.monitorFocus != monitorPaneSaved {
		t.Errorf("monitorFocus changed by a protocol-switching hotkey: got %v", m.monitorFocus)
	}
	if m.app.UI.MonitorSavedPacketsRatio != 0.5 {
		t.Errorf("adjustable split ratio changed by a protocol-switching hotkey: got %v, want 0.5 unchanged", m.app.UI.MonitorSavedPacketsRatio)
	}
}
