package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/serial"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// This file is the regression suite for two real bugs confirmed via live
// socat PTY verification (see this session's final report):
//
//  1. appendEvent used to unconditionally snap m.rx.cursor to the newest
//     packet on every RX arrival, silently discarding a user's manual
//     history navigation with no indication anything had moved. Directly
//     reproduced live: browsing back to an older packet, then a new frame
//     arriving, yanked the view back to the newest every time.
//  2. activateProtocol never cleared m.rx.history on a genuine protocol
//     switch, so a stale packet — decoded against the *previous* schema —
//     kept being rendered through RenderDiagram(*m.activeSchema, ...)
//     using the *new* schema's field layout: empty field cells, the old
//     packet's raw bytes shown under the new protocol's name, and a
//     CRC PASS/FAIL that meant nothing for the schema being displayed.
//     Directly reproduced live switching a 4-byte protocol to an unrelated
//     14-byte one.
//
// Also covers the other §1-§10 questions the investigation confirmed were
// already correct, as characterization tests: bad CRC reaches history and
// shows FAIL rather than being dropped, TX never appears as RX, the event
// pump survives a protocol-switching reconnect, and same-protocol
// activation is a genuine no-op that never clears existing history.

func rxTestSchemaSmall() packet.Schema {
	return packet.Schema{
		Name:      "small",
		TotalSize: 4,
		Fields: []packet.Field{
			{Name: "cmd", Size: 1, Format: packet.FormatUint},
			{Name: "arg", Size: 3, Format: packet.FormatHex},
		},
		Checksum: checksum.Definition{Mode: checksum.ModeNone},
	}
}

// buildValidFrame serializes sc's demo values (txTestSchema's own fixture
// shape) through packet.Serialize — the same real serializer production
// code uses — never hand-guessed bytes.
func buildValidFrame(t *testing.T, sc packet.Schema) []byte {
	t.Helper()
	raw, _, err := packet.Serialize(sc, packet.Values{
		"header":   {0xAA, 0x55},
		"command":  {0x02},
		"address":  {0x00, 0xC0, 0x17, 0xFF},
		"value":    {0xFF, 0xFF, 0x01, 0x00},
		"reserved": {0x00, 0x00},
	}, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return raw
}

func rxEvent(data []byte) tea.Msg {
	return sessionEventMsg(session.Event{Kind: session.EventRX, Data: data, Timestamp: time.Now()})
}

// --- complete frame reaches Inspector, correctly decoded --------------------

func TestRXInspectorReceivesCompleteFrame(t *testing.T) {
	m := newTestModel(t)
	sc := txTestSchema()
	m.activeSchema = &sc
	raw := buildValidFrame(t, sc)

	m.Update(rxEvent(raw))

	if len(m.rx.history) != 1 {
		t.Fatalf("rx.history = %d entries, want 1", len(m.rx.history))
	}
	pkt := m.rx.history[0]
	if string(pkt.Raw) != string(raw) {
		t.Errorf("decoded packet's raw bytes = % X, want % X", pkt.Raw, raw)
	}
	var cmdVal []byte
	for _, fv := range pkt.Fields {
		if fv.Field.Name == "command" {
			cmdVal = fv.Raw
		}
	}
	if string(cmdVal) != string([]byte{0x02}) {
		t.Errorf("decoded \"command\" field = % X, want 02", cmdVal)
	}
}

func TestRXInspectorValidCRCShowsPass(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.tab, m.packetsView = tabPackets, packetsRX
	sc := txTestSchema()
	m.activeSchema = &sc
	raw := buildValidFrame(t, sc)

	m.Update(rxEvent(raw))

	if m.rx.history[0].CRC == nil || !m.rx.history[0].CRC.Valid {
		t.Fatalf("expected a valid CRC result, got %+v", m.rx.history[0].CRC)
	}
	out := m.viewRX()
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected PASS in the rendered view:\n%s", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("did not expect FAIL for a valid CRC:\n%s", out)
	}
}

// --- bad CRC is not silently dropped ----------------------------------------

func TestRXInspectorInvalidCRCStillReachesHistoryShowsFail(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	m.tab, m.packetsView = tabPackets, packetsRX
	sc := txTestSchema()
	m.activeSchema = &sc
	raw := buildValidFrame(t, sc)
	raw[len(raw)-1] ^= 0xFF // corrupt only the CRC byte

	m.Update(rxEvent(raw))

	if len(m.rx.history) != 1 {
		t.Fatalf("a bad-CRC frame must still reach history (not be dropped), got %d entries", len(m.rx.history))
	}
	pkt := m.rx.history[0]
	if pkt.CRC == nil || pkt.CRC.Valid {
		t.Fatalf("expected an invalid CRC result, got %+v", pkt.CRC)
	}
	// Fields must still be decoded despite the bad CRC.
	if len(pkt.Fields) == 0 {
		t.Error("expected decoded fields even though the CRC is invalid")
	}
	out := m.viewRX()
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL in the rendered view:\n%s", out)
	}
}

// --- multiple frames ----------------------------------------------------------

func TestRXInspectorMultipleFramesAllCaptured(t *testing.T) {
	m := newTestModel(t)
	sc := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(sc); err != nil {
		t.Fatal(err)
	}
	m.activeSchema = &sc

	m.Update(rxEvent([]byte{0x01, 0xAA, 0xAA, 0xAA}))
	m.Update(rxEvent([]byte{0x02, 0xBB, 0xBB, 0xBB}))
	m.Update(rxEvent([]byte{0x03, 0xCC, 0xCC, 0xCC}))

	if len(m.rx.history) != 3 {
		t.Fatalf("rx.history = %d entries, want 3", len(m.rx.history))
	}
	for i, want := range []byte{0x01, 0x02, 0x03} {
		if m.rx.history[i].Raw[0] != want {
			t.Errorf("history[%d] first byte = %02X, want %02X (order preserved)", i, m.rx.history[i].Raw[0], want)
		}
	}
}

// --- cursor follow-latest / manual navigation preserved ----------------------

func TestRXInspectorCursorFollowsLatestByDefault(t *testing.T) {
	m := newTestModel(t)
	sc := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(sc); err != nil {
		t.Fatal(err)
	}
	m.activeSchema = &sc

	m.Update(rxEvent([]byte{0x01, 0, 0, 0}))
	m.Update(rxEvent([]byte{0x02, 0, 0, 0}))

	if m.rx.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (the newest packet)", m.rx.cursor)
	}
	if !m.rx.followLatest {
		t.Error("followLatest should default to true")
	}
}

// TestRXInspectorManualNavigationSurvivesNewArrival is the literal
// regression test for the confirmed bug: browsing to an older packet,
// then a new frame arriving, must not silently move the cursor.
func TestRXInspectorManualNavigationSurvivesNewArrival(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsRX
	sc := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(sc); err != nil {
		t.Fatal(err)
	}
	m.activeSchema = &sc

	m.Update(rxEvent([]byte{0x01, 0, 0, 0}))
	m.Update(rxEvent([]byte{0x02, 0, 0, 0}))
	m.Update(rxEvent([]byte{0x03, 0, 0, 0}))
	if m.rx.cursor != 2 {
		t.Fatalf("test setup: cursor = %d, want 2 (newest of 3)", m.rx.cursor)
	}

	m.updateRX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}) // browse to packet #2 (index 1)
	m.updateRX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}) // browse to packet #1 (index 0)
	if m.rx.cursor != 0 || m.rx.followLatest {
		t.Fatalf("test setup: expected cursor=0, followLatest=false, got cursor=%d followLatest=%v", m.rx.cursor, m.rx.followLatest)
	}

	m.Update(rxEvent([]byte{0x04, 0, 0, 0})) // a new frame arrives mid-inspection

	if m.rx.cursor != 0 {
		t.Errorf("cursor moved from 0 to %d after a new arrival while manually browsing — the view was yanked away", m.rx.cursor)
	}
	if len(m.rx.history) != 4 {
		t.Fatalf("rx.history = %d entries, want 4 (the new frame must still be captured)", len(m.rx.history))
	}
	out := m.viewRX()
	if !strings.Contains(out, "Packet #1/4") {
		t.Errorf("expected the view to still show packet #1/4 (unmoved), got:\n%s", out)
	}
	if !strings.Contains(out, "+3 new") {
		t.Errorf("expected a \"+3 new\" indicator (3 packets arrived ahead of the current position), got:\n%s", out)
	}
}

func TestRXInspectorScrollingToLatestReengagesFollow(t *testing.T) {
	m := newTestModel(t)
	sc := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(sc); err != nil {
		t.Fatal(err)
	}
	m.activeSchema = &sc
	m.Update(rxEvent([]byte{0x01, 0, 0, 0}))
	m.Update(rxEvent([]byte{0x02, 0, 0, 0}))

	m.updateRX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}) // away from latest
	if m.rx.followLatest {
		t.Fatal("test setup: followLatest should be false after browsing up")
	}
	m.updateRX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}) // back down to the newest
	if !m.rx.followLatest {
		t.Fatal("scrolling back down to the newest entry should re-engage followLatest")
	}

	m.Update(rxEvent([]byte{0x03, 0, 0, 0}))
	if m.rx.cursor != 2 {
		t.Errorf("cursor = %d, want 2 — a new arrival should track the latest again once re-engaged", m.rx.cursor)
	}
}

func TestRXInspectorClearResetsFollowLatest(t *testing.T) {
	m := newTestModel(t)
	sc := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(sc); err != nil {
		t.Fatal(err)
	}
	m.activeSchema = &sc
	m.Update(rxEvent([]byte{0x01, 0, 0, 0}))
	m.updateRX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})

	m.updateRX(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})

	if len(m.rx.history) != 0 || m.rx.cursor != 0 || !m.rx.followLatest {
		t.Errorf("after clear: history=%d cursor=%d followLatest=%v, want 0/0/true", len(m.rx.history), m.rx.cursor, m.rx.followLatest)
	}
}

// --- protocol switch must not leave stale decode context ---------------------

// TestRXInspectorProtocolSwitchClearsStaleHistory is the literal
// regression test for the confirmed bug: a packet decoded under protocol A
// must never be re-rendered through protocol B's field layout after a
// switch.
func TestRXInspectorProtocolSwitchClearsStaleHistory(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsRX
	small := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(small); err != nil {
		t.Fatal(err)
	}
	big := txTestSchema() // "demo", 14 bytes, unrelated field names

	m.activateProtocol(&small)
	m.Update(rxEvent([]byte{0x01, 0xAA, 0xBB, 0xCC}))
	if len(m.rx.history) != 1 {
		t.Fatalf("test setup: expected 1 packet under \"small\"")
	}

	m.activateProtocol(&big) // genuine switch, disconnected (pointer-only branch)

	if len(m.rx.history) != 0 {
		t.Fatalf("protocol switch should clear stale history, got %d entries still present", len(m.rx.history))
	}
	if !m.rx.followLatest {
		t.Error("protocol switch should leave followLatest re-engaged")
	}
	out := m.viewRX()
	if strings.Contains(out, "01 AA BB CC") {
		t.Errorf("the stale \"small\"-protocol packet's raw bytes must not appear under the new protocol, got:\n%s", out)
	}
	if !strings.Contains(out, "Waiting for packets") {
		t.Errorf("expected the fresh empty state after a protocol switch, got:\n%s", out)
	}

	// A new frame under the new protocol decodes correctly, not against
	// the old schema.
	raw := buildValidFrame(t, big)
	m.Update(rxEvent(raw))
	if len(m.rx.history) != 1 {
		t.Fatalf("expected exactly 1 packet after the switch, got %d", len(m.rx.history))
	}
	if string(m.rx.history[0].Raw) != string(raw) {
		t.Errorf("packet decoded after the switch has wrong raw bytes: % X, want % X", m.rx.history[0].Raw, raw)
	}
}

// TestRXInspectorSameProtocolNoOpPreservesHistory is the complementary
// check: activateProtocol's same-protocol no-op branch must NOT clear
// history — only a genuine change does.
func TestRXInspectorSameProtocolNoOpPreservesHistory(t *testing.T) {
	m := newTestModel(t)
	sc := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(sc); err != nil {
		t.Fatal(err)
	}
	m.activateProtocol(&sc)
	m.Update(rxEvent([]byte{0x01, 0, 0, 0}))
	if len(m.rx.history) != 1 {
		t.Fatalf("test setup: expected 1 packet")
	}

	m.activateProtocol(&sc) // same protocol again — must be a no-op

	if len(m.rx.history) != 1 {
		t.Errorf("a same-protocol no-op activation must not clear history, got %d entries", len(m.rx.history))
	}
}

// --- TX must never masquerade as RX -------------------------------------------

func TestRXInspectorTXDoesNotAppearAsRX(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received

	if len(m.rx.history) != 0 {
		t.Errorf("a local TX send must never populate rx.history, got %d entries", len(m.rx.history))
	}
}

// --- event pump survives a protocol-switching reconnect -----------------------

// TestRXInspectorEventPumpSurvivesProtocolSwitch drives a real
// session+FakePort pipeline (not a hand-constructed Msg) through a genuine
// protocol-switching reconnect and confirms RX events after the switch
// still reach the Inspector — the exact class of bug a discarded
// listenSession() Cmd caused in an earlier session.
func TestRXInspectorEventPumpSurvivesProtocolSwitch(t *testing.T) {
	m := newTestModel(t)
	small := rxTestSchemaSmall()
	if err := m.cfg.Protocols.Put(small); err != nil {
		t.Fatal(err)
	}
	big := txTestSchema()

	var mu sync.Mutex
	var curDev *serial.FakeDevice
	origOpen := serialOpenFunc
	serialOpenFunc = func(path string, cfg serial.Config) (serial.Port, error) {
		port, dev := serial.NewFakePort()
		mu.Lock()
		curDev = dev
		mu.Unlock()
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
	getDev := func() *serial.FakeDevice {
		mu.Lock()
		defer mu.Unlock()
		return curDev
	}

	sc := small
	m.connect("/fake", serial.Config{}, &sc, connectReasonNew)

	go func() { _, _ = getDev().Write([]byte{0x01, 0x02, 0x03, 0x04}) }()
	e := waitTUIEvent(t, m.sess.Events(), session.EventRX, 2*time.Second)
	m.Update(sessionEventMsg(e))
	if len(m.rx.history) != 1 {
		t.Fatalf("test setup: expected 1 packet before the switch, got %d", len(m.rx.history))
	}

	// activateProtocol's returned Cmd is listenSession()'s own blocking
	// "wait for the next event" closure — meant to be run by bubbletea's
	// runtime asynchronously, never invoked synchronously in a test (it
	// would block until an event arrives). Its propagation is already
	// covered by protocolactivation_test.go; here we only need the new
	// session itself to be live, which the reconnect below already
	// guarantees via m.sess.
	if cmd := m.activateProtocol(&big); cmd == nil { // genuine switch while connected -> reconnect
		t.Fatal("expected activateProtocol to return a reconnect Cmd")
	}

	if len(m.rx.history) != 0 {
		t.Fatalf("protocol switch should have cleared history, got %d entries", len(m.rx.history))
	}

	raw := buildValidFrame(t, big)
	go func() { _, _ = getDev().Write(raw) }()
	e2 := waitTUIEvent(t, m.sess.Events(), session.EventRX, 2*time.Second)
	if len(e2.Data) != len(raw) {
		t.Fatalf("post-switch framed RX = %d bytes, want %d (the new schema's TotalSize)", len(e2.Data), len(raw))
	}
	m.Update(sessionEventMsg(e2))
	if len(m.rx.history) != 1 {
		t.Fatalf("expected the event pump to still deliver after the reconnect, got %d entries", len(m.rx.history))
	}
}

// --- global navigation remains intact from RX Inspector -----------------------

func TestGlobalNavigationWorksFromRXInspector(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsRX

	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != tabDevices {
		t.Errorf("Tab from Packets/RX = tab %v, want tabDevices", m.tab)
	}

	m.tab, m.packetsView = tabPackets, packetsRX
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if !m.quit {
		t.Error("q from Packets/RX should quit")
	}
}
