package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
	"github.com/vtemnyakov/serialforge/internal/serial"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// attachFakeSession wires m.sess to a FakePort with raw framing and starts
// draining the FakeDevice side in the background (both directions of
// serial.FakePort are unbuffered — see its doc comment — so a Send would
// otherwise deadlock with nothing reading the peer). received delivers
// every byte slice the "device" side actually saw, in order.
//
// Also stubs serialOpenFunc (model.go) for the duration of the test: since
// sendSavedPacket started routing through model.activateProtocol, a Saved
// Packet send/hotkey can trigger connect()'s reconnect-to-reframe path
// (activateProtocol reconnects whenever m.sess != nil, to keep the live
// session's RX framing in sync with the newly active protocol — see
// model.go's activateProtocol doc comment) even from a test that never
// called connect() itself. Without this stub that reconnect would try to
// open a real "/fake" device and fail, silently clearing m.sess. Every
// FakePort the stub hands out (the initial one and any later ones from a
// reconnect) drains into the same received channel, so a test can send,
// trigger a protocol-switching reconnect, and keep reading from the one
// channel across it.
func attachFakeSession(t *testing.T, m *model) (received chan []byte) {
	t.Helper()
	received = make(chan []byte, 8)

	drain := func(dev *serial.FakeDevice) {
		t.Cleanup(func() { dev.Close() })
		go func() {
			buf := make([]byte, 256)
			for {
				n, err := dev.Read(buf)
				if n > 0 {
					got := make([]byte, n)
					copy(got, buf[:n])
					received <- got
				}
				if err != nil {
					return
				}
			}
		}()
	}

	origOpen := serialOpenFunc
	serialOpenFunc = func(path string, cfg serial.Config) (serial.Port, error) {
		port, dev := serial.NewFakePort()
		drain(dev)
		return port, nil
	}
	t.Cleanup(func() { serialOpenFunc = origOpen })

	port, dev := serial.NewFakePort()
	f, err := framing.New(framing.KindRaw, framing.Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(session.Config{Port: port, Framer: f})
	sess.Start(context.Background())
	m.sess = sess
	m.connectedPath = "/fake"
	t.Cleanup(func() { sess.Close() })
	drain(dev)
	return received
}

// backspaceN sends n Backspace keys — used to clear a form field's
// prefilled default before typing a replacement, matching this app's
// existing convention (every form prefills, then appends on typing; see
// designer.go's dmSaveName, addDeviceForm's baud default) rather than
// select-all-on-focus.
func backspaceN(m *model, n int) {
	for i := 0; i < n; i++ {
		pressKey(m, tea.KeyBackspace)
	}
}

func savedDemoPacket(name, hotkey string) savedpacket.SavedPacket {
	return savedpacket.SavedPacket{
		Name:     name,
		Protocol: "demo", // matches newTestModel's protocol
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

// --- Save / Load / Update / dirty (product spec §4/§6) ----------------------

func TestTXBuilderSaveCreatesSavedPacket(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	fillTXValues(&m.tx)

	m.updateTX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if m.tx.saveForm == nil {
		t.Fatal("'s' should open the Save-packet form")
	}
	backspaceN(m, len(m.tx.saveForm.values[0])) // clear the prefilled schema name
	typeString(m, "Get Status")
	pressKey(m, tea.KeyTab)
	typeString(m, "'")
	pressKey(m, tea.KeyEnter)

	if m.tx.saveForm != nil {
		t.Fatal("save form should close after a successful submit")
	}
	if m.tx.savedName != "Get Status" || m.tx.dirty {
		t.Errorf("after save: savedName=%q dirty=%v, want %q/false", m.tx.savedName, m.tx.dirty, "Get Status")
	}
	sp, ok := m.cfg.SavedPackets.Get("Get Status")
	if !ok {
		t.Fatal("saved packet was not persisted to the store")
	}
	if sp.Protocol != "demo" || sp.Hotkey != "'" || sp.Values["command"] != "02" {
		t.Errorf("unexpected saved packet: %+v", sp)
	}
	if sp.CRCMode != savedpacket.CRCModeAuto {
		t.Errorf("CRCMode = %q, want auto", sp.CRCMode)
	}
}

func TestTXBuilderSaveRejectsReservedHotkey(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	fillTXValues(&m.tx)

	m.updateTX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	typeString(m, "Bad")
	pressKey(m, tea.KeyTab)
	typeString(m, "q") // reserved for Quit
	pressKey(m, tea.KeyEnter)

	if m.tx.saveForm == nil {
		t.Fatal("form should stay open on a rejected hotkey, showing the error")
	}
	if !strings.Contains(m.tx.saveForm.message, "Quit") {
		t.Errorf("expected a message explaining 'q' is reserved for Quit, got %q", m.tx.saveForm.message)
	}
	if _, ok := m.cfg.SavedPackets.Get("Bad"); ok {
		t.Error("a rejected save must not persist anything")
	}
}

func TestTXBuilderOverrideCRCSavedAsOverride(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	fillTXValues(&m.tx)
	m.tx.crcOverride = "42"

	m.updateTX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	backspaceN(m, len(m.tx.saveForm.values[0]))
	typeString(m, "Faulty")
	pressKey(m, tea.KeyTab)
	pressKey(m, tea.KeyEnter) // no hotkey

	sp, ok := m.cfg.SavedPackets.Get("Faulty")
	if !ok {
		t.Fatal("saved packet not persisted")
	}
	if sp.CRCMode != savedpacket.CRCModeOverride || sp.CRCOverride != "42" {
		t.Errorf("expected CRC override 42 preserved, got %+v", sp)
	}
}

// TestLoadDoesNotAutoMutateOriginal pins §6's central distinction: loading a
// Saved Packet into TX Builder and editing it there must never change the
// persisted Saved Packet until an explicit Update.
func TestLoadDoesNotAutoMutateOriginal(t *testing.T) {
	m := newTestModel(t)
	orig := savedDemoPacket("get-status", "'")
	if err := m.cfg.SavedPackets.Put(orig); err != nil {
		t.Fatal(err)
	}
	_ = m.cfg.SavedPackets.Save()

	m.loadSavedPacketIntoTX(orig)
	if m.tx.savedName != "get-status" || m.tx.dirty {
		t.Fatalf("after load: savedName=%q dirty=%v, want get-status/false", m.tx.savedName, m.tx.dirty)
	}
	if m.packetsView != packetsTX {
		t.Fatalf("load should switch to TX Builder, packetsView=%d", m.packetsView)
	}

	// Edit a field — this must mark the TX session dirty but not touch the store.
	m.tab, m.packetsView = tabPackets, packetsTX
	m.tx.fieldCursor = 1 // "command"
	m.updateTX(tea.KeyMsg{Type: tea.KeyEnter})
	backspaceN(m, len(m.tx.editBuf)) // clear the prefilled current value ("02")
	typeString(m, "FF")
	pressKey(m, tea.KeyEnter)

	if !m.tx.dirty {
		t.Error("editing a field after load should mark the TX session dirty")
	}
	unchanged, _ := m.cfg.SavedPackets.Get("get-status")
	if unchanged.Values["command"] != "02" {
		t.Errorf("original saved packet mutated by an in-progress edit: %+v", unchanged)
	}

	// Now explicitly update — only now should the store change.
	m.updateSavedPacket()
	if m.tx.dirty {
		t.Error("dirty should clear after Update")
	}
	updated, _ := m.cfg.SavedPackets.Get("get-status")
	if updated.Values["command"] != "FF" {
		t.Errorf("Update did not persist the edited value: %+v", updated)
	}
	if updated.Hotkey != "'" {
		t.Errorf("Update should preserve the existing hotkey, got %q", updated.Hotkey)
	}
}

func TestChoosingADifferentProtocolClearsSavedName(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	m.loadSavedPacketIntoTX(sp)
	if m.tx.savedName == "" {
		t.Fatal("test setup: expected savedName to be set after load")
	}

	m.tab, m.packetsView = tabPackets, packetsTX
	m.updateTX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m.tx.handlePicker(m, tea.KeyMsg{Type: tea.KeyEnter}) // picks the only ("demo") protocol

	if m.tx.savedName != "" || m.tx.dirty {
		t.Errorf("choosing a protocol should start a fresh, non-dirty, unsaved TX session, got savedName=%q dirty=%v", m.tx.savedName, m.tx.dirty)
	}
}

// --- Saved Packets screen: send, hotkeys, duplicate/rename/delete ----------

func TestSendSavedPacketDirectSendsExactBytes(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	received := attachFakeSession(t, m)

	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0
	m.updateSaved(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	got := <-received
	pkt, err := sp.Build(m.cfg.Protocols)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if string(got) != string(pkt.Raw) {
		t.Errorf("sent % X, want % X", got, pkt.Raw)
	}
	if !strings.Contains(m.status, "get-status") {
		t.Errorf("status should name the packet, got %q", m.status)
	}
}

func TestSendSavedPacketNotConnected(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	// No session attached.
	m.sendSavedPacket(sp, "")
	if !strings.Contains(m.status, "not connected") {
		t.Errorf("status = %q, want it to say not connected", m.status)
	}
}

func TestSendSavedPacketProtocolMissing(t *testing.T) {
	m := newTestModel(t)
	sp := savedpacket.SavedPacket{Name: "ghost", Protocol: "does-not-exist"}
	_ = m.cfg.SavedPackets.Put(sp)
	m.sendSavedPacket(sp, "")
	if !strings.Contains(m.status, "protocol") {
		t.Errorf("status = %q, want a protocol-missing style message", m.status)
	}
}

// TestHotkeySendsExactlyOnePacketPerKeypress drives the actual global
// dispatch path (model.handleKey) with a bound hotkey from Navigation mode
// and confirms one keypress produces exactly one transmitted packet — the
// product's core "press the hotkey, one complete packet goes out" claim.
func TestHotkeySendsExactlyOnePacketPerKeypress(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "'")
	_ = m.cfg.SavedPackets.Put(sp)
	received := attachFakeSession(t, m)
	m.tab = tabMonitor // any Navigation-mode screen — hotkeys are global, see keybindings.go

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	got1 := <-received

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	got2 := <-received

	pkt, _ := sp.Build(m.cfg.Protocols)
	if string(got1) != string(pkt.Raw) || string(got2) != string(pkt.Raw) {
		t.Errorf("expected two identical packets % X, got % X and % X", pkt.Raw, got1, got2)
	}
	if !strings.Contains(m.status, "'") || !strings.Contains(m.status, "sent") {
		t.Errorf("hotkey-fired status should show the key and sent-confirmation, got %q", m.status)
	}
}

// TestHotkeySuppressedWhileEditing is the other half of the same claim:
// while a text-entry form is open anywhere, the same key must be consumed
// by that form (typed into the field), never interpreted as a Saved Packet
// hotkey.
func TestHotkeySuppressedWhileEditing(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "y") // 'y' is in the hotkey palette and a plausible field character too
	_ = m.cfg.SavedPackets.Put(sp)
	received := attachFakeSession(t, m)

	// Open the designer's field-name text entry — a real "typing a field
	// name" text-entry context (product spec §9's explicit example).
	m.tab, m.packetsView = tabPackets, packetsDesigner
	m.designer.openFieldForm(-1)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

	if m.designer.fieldName != "y" {
		t.Errorf("'y' should have been typed into the field-name form, got fieldName=%q", m.designer.fieldName)
	}
	select {
	case got := <-received:
		t.Errorf("hotkey must not fire while a text-entry form is open, but a packet was sent: % X", got)
	default:
		// correct: nothing sent
	}
}

func TestHotkeyResumesAfterReturningToNavigationMode(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "y")
	_ = m.cfg.SavedPackets.Put(sp)
	received := attachFakeSession(t, m)

	m.tab, m.packetsView = tabPackets, packetsDesigner
	m.designer.openFieldForm(-1)
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}) // typed, not sent
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})                       // back to Navigation mode
	if m.designer.mode != dmBrowse {
		t.Fatalf("esc should return the designer to browse mode, mode=%v", m.designer.mode)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := <-received
	pkt, _ := sp.Build(m.cfg.Protocols)
	if string(got) != string(pkt.Raw) {
		t.Errorf("expected the hotkey to send once back in Navigation mode, got % X want % X", got, pkt.Raw)
	}
}

func TestHotkeyAssignmentPersistsAndPreventsCollision(t *testing.T) {
	m := newTestModel(t)
	a := savedDemoPacket("get-status", "'")
	b := savedDemoPacket("reset", "")
	_ = m.cfg.SavedPackets.Put(a)
	_ = m.cfg.SavedPackets.Put(b)

	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 1 // "reset"
	m.updateSaved(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	if m.saved.mode != savedFormHotkey {
		t.Fatal("'h' should open the hotkey-assignment form")
	}
	typeString(m, "'") // already used by get-status
	pressKey(m, tea.KeyEnter)
	if m.saved.mode != savedFormHotkey {
		t.Fatal("colliding hotkey assignment should keep the form open with an error")
	}
	if !strings.Contains(m.saved.form.message, "get-status") {
		t.Errorf("collision message should name the conflicting packet, got %q", m.saved.form.message)
	}

	// Clear the buffer and assign a free key instead.
	m.saved.form.values[0] = ""
	typeString(m, "y")
	pressKey(m, tea.KeyEnter)
	if m.saved.mode != savedBrowse {
		t.Fatal("a valid hotkey assignment should close the form")
	}
	reset, _ := m.cfg.SavedPackets.Get("reset")
	if reset.Hotkey != "y" {
		t.Errorf("Hotkey = %q, want y", reset.Hotkey)
	}
}

func TestSavedPacketsDuplicateRenameDelete(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "'")
	_ = m.cfg.SavedPackets.Put(sp)
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0

	// Duplicate.
	m.updateSaved(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	backspaceN(m, len(m.saved.form.values[0])) // clear the prefilled "get-status copy"
	typeString(m, "get-status-2")
	pressKey(m, tea.KeyEnter)
	dup, ok := m.cfg.SavedPackets.Get("get-status-2")
	if !ok {
		t.Fatal("duplicate was not created")
	}
	if dup.Hotkey != "" {
		t.Errorf("duplicate must not inherit the hotkey, got %q", dup.Hotkey)
	}

	// Rename the original.
	m.saved.cursor = 0
	m.updateSaved(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	backspaceN(m, len(m.saved.form.values[0])) // clear the prefilled "get-status"
	typeString(m, "status")
	pressKey(m, tea.KeyEnter)
	if _, ok := m.cfg.SavedPackets.Get("get-status"); ok {
		t.Error("old name should no longer resolve after rename")
	}
	if _, ok := m.cfg.SavedPackets.Get("status"); !ok {
		t.Error("renamed packet not found under its new name")
	}

	// Delete it.
	m.saved.cursor = 0
	m.updateSaved(tea.KeyMsg{Type: tea.KeyDelete})
	pressKey(m, tea.KeyEnter) // confirm
	if _, ok := m.cfg.SavedPackets.Get("status"); ok {
		t.Error("saved packet should be gone after confirmed delete")
	}
}

// --- macOS-friendly delete key (Backspace alongside forward Delete) --------
//
// A normal Mac keyboard's Backspace-shaped key sends bubbletea's
// tea.KeyBackspace ("backspace" — mapped from the ASCII DEL byte most
// terminals actually emit for it), not tea.KeyDelete ("delete", the
// forward-delete key reached via Fn+Delete on a Mac laptop). Both must
// trigger the exact same confirm-delete flow — see the "delete", "backspace"
// case in updateSaved.

// 1. Backspace deletes from Saved Packets navigation via confirmation.
func TestSavedPacketsBackspaceTriggersDeleteConfirmation(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0

	m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.saved.mode != savedConfirmDelete {
		t.Fatalf("backspace in Saved Packets navigation should open the confirm-delete modal, got mode=%v", m.saved.mode)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	if m.saved.mode != savedBrowse {
		t.Fatalf("mode after confirming = %v, want savedBrowse", m.saved.mode)
	}
	if _, ok := m.cfg.SavedPackets.Get("get-status"); ok {
		t.Error("saved packet should be gone after backspace + confirm")
	}
}

// 2. Delete does the same — same code path, not a second implementation.
func TestSavedPacketsDeleteKeyTriggersDeleteConfirmation(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0

	m.handleKey(tea.KeyMsg{Type: tea.KeyDelete})
	if m.saved.mode != savedConfirmDelete {
		t.Fatalf("delete key in Saved Packets navigation should open the confirm-delete modal, got mode=%v", m.saved.mode)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	if _, ok := m.cfg.SavedPackets.Get("get-status"); ok {
		t.Error("saved packet should be gone after delete + confirm")
	}
}

// 3. Backspace inside a text-entry/edit mode edits text and does not
// trigger packet deletion — the rename form's own handleKey claims
// Backspace first (savedState.handleKeyIfEditing intercepts before
// updateSaved's browse dispatch ever sees the key), so it must never reach
// the "delete"/"backspace" browse-mode case.
func TestSavedPacketsBackspaceEditsTextInsteadOfDeletingWhileFormOpen(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0

	// Open the rename form (prefilled with the current name) — a
	// text-entry mode, not Navigation-mode browse.
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if m.saved.form == nil || m.saved.mode != savedFormRename {
		t.Fatal("'r' should open the rename form")
	}
	before := m.saved.form.values[0]
	if before != "get-status" {
		t.Fatalf("rename form should prefill the current name, got %q", before)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})

	if m.saved.mode != savedFormRename || m.saved.form == nil {
		t.Fatalf("backspace while the rename form is open must edit text, not trigger delete — got mode=%v", m.saved.mode)
	}
	if got, want := m.saved.form.values[0], before[:len(before)-1]; got != want {
		t.Errorf("backspace should remove the last character of the name buffer, got %q, want %q", got, want)
	}
	if _, ok := m.cfg.SavedPackets.Get("get-status"); !ok {
		t.Error("the saved packet must still exist — backspace in a text field must never delete a packet")
	}
}

// 4. The hint renders correctly at narrow widths.
func TestSavedPacketsDeleteHintRendersAtNarrowWidth(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)
	m.tab, m.packetsView = tabPackets, packetsSaved
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m = next.(*model)

	out := m.viewSaved()
	if out == "" {
		t.Fatal("narrow Saved Packets view rendered empty")
	}
	if !strings.Contains(out, "⌫") {
		t.Errorf("Saved Packets hint should show the cross-platform delete glyph, not imply a dedicated forward-Delete key; got:\n%s", out)
	}

	// Isolate just the hint bar's own rendered width — a realistic bound
	// for a narrow terminal, independent of the list/detail body above it.
	deleteHint := renderHints(hint("⌫/Del", "remove"))
	if w := lipgloss.Width(deleteHint); w == 0 || w > 20 {
		t.Errorf("delete hint visible width = %d, want a small positive width suitable for narrow terminals", w)
	}
}

// --- rendering ---------------------------------------------------------------

func TestViewSavedRendersListAndDetail(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsSaved
	sp := savedDemoPacket("get-status", "'")
	_ = m.cfg.SavedPackets.Put(sp)

	out := m.viewSaved()
	if !strings.Contains(out, "get-status") {
		t.Errorf("expected saved packet name in list, got:\n%s", out)
	}
	if !strings.Contains(out, "'") {
		t.Errorf("expected hotkey shown in list, got:\n%s", out)
	}
	if !strings.Contains(out, "demo") {
		t.Errorf("expected protocol name in list, got:\n%s", out)
	}
	if !strings.Contains(out, "AUTO") {
		t.Errorf("expected CRC AUTO in detail panel, got:\n%s", out)
	}
}

func TestViewSavedShowsBrokenState(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsSaved
	sp := savedpacket.SavedPacket{Name: "ghost", Protocol: "does-not-exist"}
	_ = m.cfg.SavedPackets.Put(sp)

	out := m.viewSaved()
	if !strings.Contains(out, "missing") {
		t.Errorf("expected a 'protocol missing' style message, got:\n%s", out)
	}
}

func TestViewSavedNarrowWidthDoesNotPanic(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.width = 20
	sp := savedDemoPacket("a-fairly-long-saved-packet-name-here", "'")
	_ = m.cfg.SavedPackets.Put(sp)
	if out := m.viewSaved(); out == "" {
		t.Error("expected non-empty output at narrow width")
	}
}
