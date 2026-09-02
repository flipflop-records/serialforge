package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/session"
)

// This file is the regression suite for the "Monitor doesn't show local
// TX" bug: every interactive send path — TX Builder, Saved Packet
// hotkey/direct-send, Monitor sidebar Enter — now funnels through
// model.sendTX (savedpackets.go/txrx.go), which records a Monitor TX
// event synchronously the instant session.Send succeeds, independent of
// whether the session's own async Events() pump happens to be running.
// See model.go's sendTX doc comment and ARCHITECTURE.md "TX/RX Monitor
// event recording" for the full rationale (this is also what the
// unnecessary-reconnect fix in activateProtocol was hiding: every
// reconnect used to silently kill that pump for good).

func countEventKind(m *model, kind session.EventKind) int {
	n := 0
	for _, e := range m.events {
		if e.event.Kind == kind {
			n++
		}
	}
	return n
}

func lastEventOfKind(t *testing.T, m *model, kind session.EventKind) session.Event {
	t.Helper()
	for i := len(m.events) - 1; i >= 0; i-- {
		if m.events[i].event.Kind == kind {
			return m.events[i].event
		}
	}
	t.Fatalf("no %s event found in m.events", kind)
	return session.Event{}
}

func TestTXBuilderSendCreatesExactlyOneTXEvent(t *testing.T) {
	m := newTestModel(t)
	sc := txTestSchema()
	m.tab, m.packetsView = tabPackets, packetsTX
	m.tx.schema = &sc
	fillTXValues(&m.tx)
	received := attachFakeSession(t, m)
	m.activeSchema = &sc

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	<-received // blocks until the fake wire actually saw it

	if got := countEventKind(m, session.EventTX); got != 1 {
		t.Fatalf("TX events in m.events = %d, want exactly 1", got)
	}
}

func TestSavedPacketHotkeySendCreatesExactlyOneTXEvent(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	<-received

	if got := countEventKind(m, session.EventTX); got != 1 {
		t.Fatalf("TX events in m.events = %d, want exactly 1", got)
	}
}

func TestSavedPacketsDirectSendCreatesExactlyOneTXEvent(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)
	m.tab, m.packetsView = tabPackets, packetsSaved
	m.saved.cursor = 0

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	<-received

	if got := countEventKind(m, session.EventTX); got != 1 {
		t.Fatalf("TX events in m.events = %d, want exactly 1", got)
	}
}

func TestMonitorSidebarEnterSendCreatesExactlyOneTXEvent(t *testing.T) {
	m := newTestModel(t)
	m = setMonitorWidth(t, m, 120, 40)
	sc := txTestSchema()
	m.activeSchema = &sc
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", ""))
	received := attachFakeSession(t, m)
	m.monitorFocus = monitorPaneSaved

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	<-received

	if got := countEventKind(m, session.EventTX); got != 1 {
		t.Fatalf("TX events in m.events = %d, want exactly 1", got)
	}
}

// TestFailedSendDoesNotCreateTXEvent closes the underlying session
// immediately before sending, forcing session.Send to fail (a write to a
// closed FakePort), and confirms no TX event was recorded — only a
// *successful* Send may ever produce one (sendTX's own contract). The
// packet's protocol is made active *before* closing so activateProtocol
// sees "already active" and skips its own reconnect (which would
// otherwise transparently reopen a fresh, working connection via the test
// stub and mask the failure this test exists to check).
func TestFailedSendDoesNotCreateTXEvent(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	sc, ok := m.cfg.Protocols.Get("demo")
	if !ok {
		t.Fatal("test setup: \"demo\" protocol not found")
	}
	m.activeSchema = &sc
	attachFakeSession(t, m)
	m.activeSchema = &sc // attachFakeSession doesn't touch it, but be explicit
	m.sess.Close()       // still non-nil, but every Send on it will now fail

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})

	if got := countEventKind(m, session.EventTX); got != 0 {
		t.Errorf("TX events after a failed send = %d, want 0", got)
	}
	if !strings.Contains(m.status, "Get Status") || !strings.Contains(m.status, "failed") {
		t.Errorf("status = %q, want it to report the send failure", m.status)
	}
}

// TestTXEventBytesMatchExactlyWhatWasSent proves sendTX records the exact
// bytes actually passed to session.Send — not a re-serialized or
// independently-recomputed copy.
func TestTXEventBytesMatchExactlyWhatWasSent(t *testing.T) {
	m := newTestModel(t)
	sp := monitorDemoPacket("Get Status", "'")
	_ = m.cfg.SavedPackets.Put(sp)
	received := attachFakeSession(t, m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	onWire := <-received

	got := lastEventOfKind(t, m, session.EventTX)
	if string(got.Data) != string(onWire) {
		t.Errorf("TX event bytes = % X, want exactly the bytes on the wire % X", got.Data, onWire)
	}
}

// TestLaterMatchingRXIsStillASeparateEvent proves TX/RX are never
// deduplicated even when byte-identical — they represent different
// directions (task requirement: an echoing device legitimately produces
// both). Drives the real async pump path (Update(sessionEventMsg)) that
// RX still goes through, distinct from TX's synchronous path.
func TestLaterMatchingRXIsStillASeparateEvent(t *testing.T) {
	m := newTestModel(t)
	_ = m.cfg.SavedPackets.Put(monitorDemoPacket("Get Status", "'"))
	received := attachFakeSession(t, m)

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("'")})
	onWire := <-received
	if got := countEventKind(m, session.EventTX); got != 1 {
		t.Fatalf("test setup: TX events = %d, want 1", got)
	}

	// Simulate the device echoing the exact same bytes back — the real
	// async path an actual RX frame arrives through.
	m.Update(sessionEventMsg(session.Event{Kind: session.EventRX, Data: onWire}))

	if got := countEventKind(m, session.EventTX); got != 1 {
		t.Errorf("TX events after the echo = %d, want still exactly 1 (unchanged)", got)
	}
	if got := countEventKind(m, session.EventRX); got != 1 {
		t.Errorf("RX events after the echo = %d, want exactly 1 — TX and RX must never be deduplicated", got)
	}
	if len(m.events) != 2 {
		t.Errorf("total events = %d, want 2 (one TX, one RX, both kept)", len(m.events))
	}
}

// TestSessionEventPumpNeverDoubleCountsTX drives the async pump directly
// with a synthetic EventTX (as if the session's own Send-side emission
// were, hypothetically, also delivered through it) and confirms Update()
// discards it rather than appending a second copy — the mechanism that
// makes sendTX's synchronous recording safe to rely on exclusively.
func TestSessionEventPumpNeverDoubleCountsTX(t *testing.T) {
	m := newTestModel(t)
	m.Update(sessionEventMsg(session.Event{Kind: session.EventTX, Data: []byte{0x01}}))
	if got := countEventKind(m, session.EventTX); got != 0 {
		t.Errorf("TX events via the async pump = %d, want 0 (Update must skip EventTX — see its own comment)", got)
	}
}
