package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/device"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/protocol"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// newTestModel builds a model against a throwaway config directory — the
// same construction path cmd/serialforge's `tui` command uses, minus actually
// starting a Bubble Tea program (which needs a real TTY). This is a smoke
// test: every tab's View() must render without panicking, for a model in
// its ordinary starting state and with one saved protocol loaded, so a
// nil-pointer regression in any screen fails `go test` instead of only
// showing up when someone launches the real TUI.
func newTestModel(t *testing.T) *model {
	t.Helper()
	dir := t.TempDir()
	devices, err := device.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	protocols, err := protocol.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := savedpacket.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := device.LoadRecent(dir)
	if err != nil {
		t.Fatal(err)
	}
	sc := packet.Schema{
		Name:      "demo",
		TotalSize: 14,
		Fields: []packet.Field{
			{Name: "header", Size: 2, Format: packet.FormatHex},
			{Name: "command", Size: 1, Format: packet.FormatUint},
			{Name: "address", Size: 4, Format: packet.FormatHex},
			{Name: "value", Size: 4, Format: packet.FormatHex},
			{Name: "reserved", Size: 2, Format: packet.FormatRaw},
		},
		Checksum: checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/MAXIM-DOW"},
	}
	if err := protocols.Put(sc); err != nil {
		t.Fatal(err)
	}

	m := newModel(RunConfig{
		ConfigDir:    dir,
		App:          config.DefaultApp(),
		Devices:      devices,
		Protocols:    protocols,
		SavedPackets: saved,
		Recent:       recent,
		Version:      "test",
	})
	// A real run always gets a WindowSizeMsg before the first draw;
	// simulate that so width-dependent rendering (the diagram) is exercised.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return next.(*model)
}

func TestEveryTabRendersWithoutPanicking(t *testing.T) {
	for tab := 0; tab < tabCount; tab++ {
		m := newTestModel(t)
		m.tab = tab
		out := m.View()
		if out == "" {
			t.Errorf("tab %d (%s): View() returned empty output", tab, tabNames[tab])
		}
	}
}

func TestPacketsSubviewsRenderWithoutPanicking(t *testing.T) {
	for view := 0; view < packetsViewCount; view++ {
		m := newTestModel(t)
		m.tab = tabPackets
		m.packetsView = view
		if out := m.View(); out == "" {
			t.Errorf("packets subview %d (%s): View() returned empty output", view, packetsViewNames[view])
		}
	}
}

func TestConfigSectionsRenderWithoutPanicking(t *testing.T) {
	for section := 0; section < cfgSectionCount; section++ {
		m := newTestModel(t)
		m.tab = tabConfig
		m.cfgSection = section
		if out := m.View(); out == "" {
			t.Errorf("config section %d (%s): View() returned empty output", section, cfgSectionNames[section])
		}
	}
}

// TestSerialDefaultsSubModesRenderWithoutPanicking drives Serial Defaults
// into each of its sub-modes (picker open, custom-baud form, confirm-reset)
// and renders each — a nil-pointer regression in any of them (e.g.
// s.baudInput being nil while s.mode == sdBaudCustom) fails go test instead
// of only showing up interactively.
func TestSerialDefaultsSubModesRenderWithoutPanicking(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabConfig
	m.cfgSection = cfgSerialDefaults

	m.sd.openPicker(sfBaud)
	if out := m.View(); out == "" {
		t.Error("Serial Defaults baud picker: View() returned empty output")
	}
	m.sd.pickerCursor = len(serial.BaudPresets) // the trailing "Custom…" row
	m.sd.confirmPicker()
	if m.sd.mode != sdBaudCustom || m.sd.baudInput == nil {
		t.Fatalf("selecting Custom… should open the baud text form, got mode=%v baudInput=%v", m.sd.mode, m.sd.baudInput)
	}
	if out := m.View(); out == "" {
		t.Error("Serial Defaults custom-baud form: View() returned empty output")
	}

	m.sd.mode = sdConfirmReset
	if out := m.View(); out == "" {
		t.Error("Serial Defaults confirm-reset: View() returned empty output")
	}
}

func TestDesignerEditFlowBuildsAValidSchema(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabPackets
	m.packetsView = packetsDesigner
	d := &m.designer

	// Fresh designer starts empty/invalid.
	if d.schema.Validate() == nil {
		t.Fatal("a brand-new draft schema should not validate")
	}

	d.activateRow() // opens the total-size form (cursor starts at 0)
	if d.mode != dmTotalSize {
		t.Fatalf("mode = %v, want dmTotalSize", d.mode)
	}
	typeString(m, "4")
	pressKey(m, tea.KeyEnter)
	if d.schema.TotalSize != 4 {
		t.Fatalf("TotalSize = %d, want 4", d.schema.TotalSize)
	}

	// Add one 4-byte field via 'n', filling name then size.
	m.updateDesigner(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if d.mode != dmField {
		t.Fatalf("mode = %v, want dmField after 'n'", d.mode)
	}
	typeString(m, "DATA")
	pressKey(m, tea.KeyTab)
	typeString(m, "4")
	pressKey(m, tea.KeyEnter)

	if err := d.schema.Validate(); err != nil {
		t.Fatalf("schema should validate after a 4-byte field fills a 4-byte packet: %v", err)
	}
	if len(d.schema.Fields) != 1 || d.schema.Fields[0].Name != "DATA" || d.schema.Fields[0].Size != 4 {
		t.Fatalf("unexpected field: %+v", d.schema.Fields)
	}
}

// TestTUIStartsWithZeroDevicesAndZeroDetectedPorts pins down the exact
// scenario this was reported broken for: no saved device profiles, no
// USB-serial ports detected (only whatever built-in entries the platform
// always reports, modeled here as none at all so the test is deterministic
// across machines/CI rather than depending on the host's real ports). The
// TUI must still construct and render every tab — "no usable device" is a
// normal starting state, not a reason to fail — see Run's doc comment.
func TestTUIStartsWithZeroDevicesAndZeroDetectedPorts(t *testing.T) {
	m := newTestModel(t)
	m.detected = nil // force the deterministic zero-ports case regardless of this host's real ports
	m.devices = mustEmptyDeviceStore(t)

	if len(m.devices.All()) != 0 {
		t.Fatalf("test setup: expected zero saved profiles, got %d", len(m.devices.All()))
	}
	if len(m.detected) != 0 {
		t.Fatalf("test setup: expected zero detected ports, got %d", len(m.detected))
	}
	if m.quit {
		t.Fatal("model.quit is true immediately after construction — startup must never self-terminate")
	}

	for tab := 0; tab < tabCount; tab++ {
		m.tab = tab
		out := m.View()
		if out == "" {
			t.Errorf("tab %d (%s) with zero devices/ports: View() returned empty output", tab, tabNames[tab])
		}
		if m.quit {
			t.Errorf("tab %d (%s): rendering with zero devices/ports set model.quit", tab, tabNames[tab])
		}
	}

	// The Monitor tab specifically must explain the empty state rather than
	// silently showing nothing — this is the "polished empty state" the
	// product spec requires instead of quitting or requiring hardware.
	m.tab = tabMonitor
	if out := m.View(); !strings.Contains(out, "Not connected") {
		t.Errorf("Monitor tab with no connection should show its not-connected guidance, got:\n%s", out)
	}
}

func mustEmptyDeviceStore(t *testing.T) *device.Store {
	t.Helper()
	s, err := device.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestFriendlyStartErrorRecognizesNoTTY covers the actual root cause behind
// "serialforge exits immediately with no visible TUI": Bubble Tea's own
// os.Open("/dev/tty") fallback (used when stdin isn't a terminal) failing
// because there is no controlling terminal at all — e.g. the process was
// launched without a pty. That is unrelated to device/USB state; this test
// exercises the pure error-translation function directly since actually
// reproducing "no controlling terminal" portably inside `go test` isn't
// practical (it depends on how the test binary itself was launched).
func TestFriendlyStartErrorRecognizesNoTTY(t *testing.T) {
	ttyErr := fmt.Errorf("could not open a new TTY: %w", errors.New("open /dev/tty: device not configured"))
	got := friendlyStartError(ttyErr)
	if got == nil {
		t.Fatal("friendlyStartError(nil) for a TTY error, want a wrapped explanation")
	}
	msg := got.Error()
	for _, want := range []string{"no interactive terminal", "serialforge ports", "serialforge monitor"} {
		if !strings.Contains(msg, want) {
			t.Errorf("friendlyStartError message = %q, want it to mention %q", msg, want)
		}
	}
	if !errors.Is(got, ttyErr) {
		t.Error("friendlyStartError should wrap (not discard) the original error for %w/errors.Is")
	}
}

func TestFriendlyStartErrorPassesThroughUnrelatedErrors(t *testing.T) {
	other := errors.New("some unrelated Bubble Tea failure")
	got := friendlyStartError(other)
	if got != other {
		t.Errorf("friendlyStartError should pass unrelated errors through unchanged, got %v", got)
	}
}

func typeString(m *model, s string) {
	for _, r := range s {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func pressKey(m *model, t tea.KeyType) {
	m.handleKey(tea.KeyMsg{Type: t})
}
