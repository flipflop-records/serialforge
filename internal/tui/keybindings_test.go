package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

// navScenario is one Navigation-mode screen this feature could plausibly
// fire a hotkey from: a setup that puts a fresh test model on that screen
// (no sub-form open), and a snapshot of every field that screen's own key
// dispatch can mutate, captured as primitives (never a shared slice/map/
// pointer) so before/after comparison can never be fooled by aliasing.
type navScenario struct {
	name     string
	setup    func(m *model)
	snapshot func(m *model) string
}

var navScenarios = []navScenario{
	{"Monitor", func(m *model) { m.tab = tabMonitor }, func(m *model) string {
		return fmt.Sprintf("paused=%v mode=%s events=%d", m.paused, m.monitorMode, len(m.events))
	}},
	{"Devices", func(m *model) { m.tab = tabDevices }, func(m *model) string {
		return fmt.Sprintf("cursor=%d add=%v manual=%v virtual=%v save=%v profiles=%d",
			m.devCursor, m.devAdd != nil, m.devManual != nil, m.devVirtual != nil, m.devSave != nil, len(m.devices.All()))
	}},
	{"Batch", func(m *model) { m.tab = tabBatch }, func(m *model) string {
		b := m.batch
		return fmt.Sprintf("cursor=%d pathInput=%v pathBuf=%q running=%v scenarios=%d", b.cursor, b.pathInput, b.pathBuf, b.running, len(b.scenarios))
	}},
	{"Config", func(m *model) { m.tab = tabConfig }, func(m *model) string {
		return fmt.Sprintf("ts=%v reconnect=%v", m.app.UI.ShowTimestamps, m.app.Reconnect.Enabled)
	}},
	{"Logs", func(m *model) {
		m.tab = tabLogs
		for i := 0; i < 5; i++ {
			m.logEvent(LogInfo, "entry %d", i)
		}
	}, func(m *model) string {
		return fmt.Sprintf("scroll=%d followTail=%v entries=%d", m.logs.scroll, m.logs.followTail, len(m.appLog))
	}},
	{"Packets/Designer", func(m *model) { m.tab = tabPackets; m.packetsView = packetsDesigner }, func(m *model) string {
		d := m.designer
		return fmt.Sprintf("cursor=%d mode=%d fields=%d total=%d loaded=%q", d.cursor, d.mode, len(d.schema.Fields), d.schema.TotalSize, d.loadedName)
	}},
	{"Packets/TX", func(m *model) { m.tab = tabPackets; m.packetsView = packetsTX }, func(m *model) string {
		t := m.tx
		return fmt.Sprintf("mode=%d cursor=%d crc=%q values=%d saved=%q dirty=%v schemaNil=%v formOpen=%v",
			t.mode, t.fieldCursor, t.crcOverride, len(t.values), t.savedName, t.dirty, t.schema == nil, t.saveForm != nil)
	}},
	{"Packets/RX", func(m *model) { m.tab = tabPackets; m.packetsView = packetsRX }, func(m *model) string {
		r := m.rx
		return fmt.Sprintf("cursor=%d pickerOpen=%v history=%d", r.cursor, r.pickerOpen, len(r.history))
	}},
	{"Packets/Saved", func(m *model) { m.tab = tabPackets; m.packetsView = packetsSaved }, func(m *model) string {
		s := m.saved
		return fmt.Sprintf("cursor=%d mode=%d formOpen=%v packets=%d", s.cursor, s.mode, s.form != nil, len(m.cfg.SavedPackets.All()))
	}},
}

// TestPaletteKeysAreNeverConsumedByCoreDispatch is the mechanical
// enforcement behind keybindings.go's central design decision: hotkeyPalette
// is a permanent carve-out no core/screen keybinding may ever be added
// from. This drives every scenario's Navigation-mode dispatch (the real
// model.handleKey entry point — the exact path an actual keypress takes)
// with every palette key and asserts no observable state changed. A future
// change that accidentally binds a palette key to a core action fails this
// test instead of silently creating an unsafe hotkey collision.
func TestPaletteKeysAreNeverConsumedByCoreDispatch(t *testing.T) {
	for _, sc := range navScenarios {
		for _, key := range hotkeyPalette {
			t.Run(sc.name+"/"+key, func(t *testing.T) {
				m := newTestModel(t)
				sc.setup(m)
				before := sc.snapshot(m)
				m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
				after := sc.snapshot(m)
				if before != after {
					t.Errorf("key %q changed %s state:\n before=%s\n after=%s\npalette keys must never be consumed by core dispatch — see keybindings.go", key, sc.name, before, after)
				}
			})
		}
	}
}

func TestValidHotkeyChar(t *testing.T) {
	for _, k := range hotkeyPalette {
		if !ValidHotkeyChar(k) {
			t.Errorf("ValidHotkeyChar(%q) = false, want true (it's in hotkeyPalette)", k)
		}
	}
	for _, k := range []string{"q", "o", "enter", "tab", "1", "["} {
		if ValidHotkeyChar(k) {
			t.Errorf("ValidHotkeyChar(%q) = true, want false (reserved elsewhere)", k)
		}
	}
}

func TestValidateHotkeyAssignment(t *testing.T) {
	store := &savedpacket.Store{}
	_ = store.Put(savedpacket.SavedPacket{Name: "reset", Hotkey: "."})

	if err := ValidateHotkeyAssignment("", store, "anything"); err != nil {
		t.Errorf("empty hotkey (unbind) should always be valid, got %v", err)
	}
	if err := ValidateHotkeyAssignment("'", store, "new-packet"); err != nil {
		t.Errorf("a free palette key should be valid, got %v", err)
	}
	if err := ValidateHotkeyAssignment("q", store, "new-packet"); err == nil {
		t.Error("assigning a reserved core key should be rejected")
	} else if !strings.Contains(err.Error(), "Quit") {
		t.Errorf("rejection for 'q' should explain it's reserved for Quit, got: %v", err)
	}
	if err := ValidateHotkeyAssignment(".", store, "new-packet"); err == nil {
		t.Error("assigning a hotkey already used by another saved packet should be rejected")
	} else if !strings.Contains(err.Error(), "reset") {
		t.Errorf("collision error should name the conflicting packet, got: %v", err)
	}
	// Re-assigning a packet's own current hotkey to itself is fine.
	if err := ValidateHotkeyAssignment(".", store, "reset"); err != nil {
		t.Errorf("re-assigning a packet's own hotkey to itself should be valid, got %v", err)
	}
}
