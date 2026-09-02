package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// enterSerialDefaults puts a fresh test model on Config/Serial Defaults —
// the state every test below starts from.
func enterSerialDefaults(t *testing.T) *model {
	t.Helper()
	m := newTestModel(t)
	m.tab = tabConfig
	m.cfgSection = cfgSerialDefaults
	return m
}

func TestSerialDefaultsStartsAtBuiltInDefaults(t *testing.T) {
	m := enterSerialDefaults(t)
	eff := m.sd.effective()
	want := serial.DefaultConfig()
	if eff.Baud != want.Baud || eff.DataBits != want.DataBits || eff.Parity != want.Parity ||
		eff.StopBits != want.StopBits || eff.FlowControl != want.FlowControl {
		t.Fatalf("fresh Serial Defaults effective config = %+v, want built-in default %+v", eff, want)
	}
	if m.sd.dirty {
		t.Error("fresh Serial Defaults should not start dirty")
	}
}

func TestSerialDefaultsBaudPreset(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.cursor = sfBaud
	m.sd.openPicker(sfBaud)
	if m.sd.mode != sdPicker {
		t.Fatalf("mode = %v, want sdPicker", m.sd.mode)
	}
	// Pick the preset matching 921600 explicitly rather than assuming index.
	idx := -1
	for i, b := range serial.BaudPresets {
		if b == 921600 {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("921600 is expected to be one of the baud presets")
	}
	m.sd.pickerCursor = idx
	m.sd.confirmPicker()
	if m.sd.mode != sdBrowse {
		t.Fatalf("mode after confirming a preset = %v, want sdBrowse", m.sd.mode)
	}
	if !m.sd.dirty {
		t.Error("selecting a baud preset should mark Serial Defaults dirty")
	}
	if got := m.sd.effective().Baud; got != 921600 {
		t.Errorf("effective Baud = %d, want 921600", got)
	}
}

func TestSerialDefaultsBaudCustomValidAndInvalid(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.openPicker(sfBaud)
	m.sd.pickerCursor = len(serial.BaudPresets) // the trailing "Custom…" row
	m.sd.confirmPicker()
	if m.sd.mode != sdBaudCustom || m.sd.baudInput == nil {
		t.Fatalf("selecting Custom… should open the baud text form, got mode=%v", m.sd.mode)
	}

	// Invalid: zero.
	m.sd.baudInput.values[0] = "0"
	m.sd.handleBaudCustom(tea.KeyMsg{Type: tea.KeyEnter})
	if m.sd.mode != sdBaudCustom {
		t.Fatalf("an invalid (zero) baud should not leave the form, mode = %v", m.sd.mode)
	}
	if m.sd.baudInput.message == "" {
		t.Error("an invalid baud should set a form error message")
	}
	if m.sd.dirty {
		t.Error("an invalid baud entry should not mark Serial Defaults dirty")
	}

	// Invalid: negative.
	m.sd.baudInput.values[0] = "-115200"
	m.sd.handleBaudCustom(tea.KeyMsg{Type: tea.KeyEnter})
	if m.sd.mode != sdBaudCustom {
		t.Fatalf("a negative baud should not leave the form, mode = %v", m.sd.mode)
	}

	// Valid: an arbitrary rate not in the preset list.
	m.sd.baudInput.values[0] = "500000"
	m.sd.handleBaudCustom(tea.KeyMsg{Type: tea.KeyEnter})
	if m.sd.mode != sdBrowse {
		t.Fatalf("a valid custom baud should return to browse, mode = %v", m.sd.mode)
	}
	if !m.sd.dirty {
		t.Error("a valid custom baud should mark Serial Defaults dirty")
	}
	if got := m.sd.effective().Baud; got != 500000 {
		t.Errorf("effective Baud = %d, want 500000", got)
	}
}

func TestSerialDefaultsDataBitsParityStopBitsFlowControl(t *testing.T) {
	cases := []struct {
		name  string
		field serialField
		want  func(serial.Config) bool
	}{
		{"data bits", sfDataBits, func(c serial.Config) bool { return c.DataBits == sdDataBitsValues[len(sdDataBitsValues)-1] }},
		{"parity", sfParity, func(c serial.Config) bool { return c.Parity == sdParityValues[len(sdParityValues)-1] }},
		{"stop bits", sfStopBits, func(c serial.Config) bool { return c.StopBits == sdStopBitsValues[len(sdStopBitsValues)-1] }},
		{"flow control", sfFlowControl, func(c serial.Config) bool { return c.FlowControl == sdFlowValues[len(sdFlowValues)-1] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := enterSerialDefaults(t)
			m.sd.openPicker(tc.field)
			labels := m.sd.pickerLabels()
			m.sd.pickerCursor = len(labels) - 1 // last choice, never the built-in default's first entry
			m.sd.confirmPicker()
			if m.sd.mode != sdBrowse {
				t.Fatalf("mode after confirming = %v, want sdBrowse", m.sd.mode)
			}
			if !m.sd.dirty {
				t.Error("selecting a value should mark Serial Defaults dirty")
			}
			if !tc.want(m.sd.effective()) {
				t.Errorf("effective config after picking the last %s choice = %+v", tc.name, m.sd.effective())
			}
		})
	}
}

// TestSerialDefaultsFlowControlNeverOffersXonXoff pins task rule #8 ("do not
// add UI options the backend cannot honor"): FlowXonXoff is accepted by
// serial.Config.Validate and modeled in the type, but
// internal/serial/port.go's applyFlowControl does nothing for it — offering
// it here would be a silent no-op.
func TestSerialDefaultsFlowControlNeverOffersXonXoff(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.openPicker(sfFlowControl)
	for _, label := range m.sd.pickerLabels() {
		if strings.Contains(strings.ToLower(label), "xon") {
			t.Fatalf("flow control picker offers Xon/Xoff (%q), which the transport does not implement", label)
		}
	}
	for _, v := range sdFlowValues {
		if v == serial.FlowXonXoff {
			t.Fatal("sdFlowValues includes FlowXonXoff, which must never be offered in the UI")
		}
	}
}

func TestSerialDefaultsAutoReconnectToggles(t *testing.T) {
	m := enterSerialDefaults(t)
	start := m.sd.autoReconn
	m.sd.openPicker(sfAutoReconnect) // toggles directly, no picker
	if m.sd.mode != sdBrowse {
		t.Fatalf("toggling Auto Reconnect should stay in browse mode, got %v", m.sd.mode)
	}
	if m.sd.autoReconn == start {
		t.Error("Auto Reconnect did not toggle")
	}
	if !m.sd.dirty {
		t.Error("toggling Auto Reconnect should mark Serial Defaults dirty")
	}
}

func TestSerialDefaultsSaveAndReloadAfterRestart(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.working = config.SerialPrefs{Baud: 921600, DataBits: 7, Parity: "even", StopBits: "2", FlowControl: "rts_cts"}
	m.sd.autoReconn = false
	m.sd.dirty = true

	m.saveSerialDefaults()
	if m.sd.dirty {
		t.Error("a successful save should clear the dirty flag")
	}
	if m.sd.err != "" {
		t.Errorf("a successful save should not set an error, got %q", m.sd.err)
	}

	// "Restart": load app.yaml fresh from the same config dir, the way
	// cmd/serialforge's `tui` command does on every launch.
	reloaded, err := config.LoadApp(m.cfg.ConfigDir)
	if err != nil {
		t.Fatalf("LoadApp: %v", err)
	}
	if reloaded.Serial != m.sd.working {
		t.Errorf("reloaded.Serial = %+v, want %+v", reloaded.Serial, m.sd.working)
	}
	if reloaded.Reconnect.Enabled != false {
		t.Error("reloaded.Reconnect.Enabled should persist the edited value (false)")
	}
}

func TestSerialDefaultsSaveRejectsInvalidWithoutPersisting(t *testing.T) {
	m := enterSerialDefaults(t)
	// Not reachable through the picker UI (which only offers valid data
	// bits), but a directly-constructed invalid working copy must still be
	// rejected at save time rather than trusted blindly — defense in depth,
	// matching internal/serial.Config.Validate's own contract.
	m.sd.working = config.SerialPrefs{DataBits: 3}
	m.sd.dirty = true

	m.saveSerialDefaults()
	if m.sd.err == "" {
		t.Error("saving an invalid config should set an error")
	}
	if !m.sd.dirty {
		t.Error("a rejected save should leave the dirty flag set")
	}

	reloaded, err := config.LoadApp(m.cfg.ConfigDir)
	if err != nil {
		t.Fatalf("LoadApp: %v", err)
	}
	if reloaded.Serial.DataBits == 3 {
		t.Error("an invalid config must never actually be persisted")
	}
}

func TestSerialDefaultsResetToBuiltInDefaults(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.working = config.SerialPrefs{Baud: 921600, DataBits: 7, Parity: "even", StopBits: "2", FlowControl: "rts_cts"}
	m.sd.autoReconn = false
	m.sd.dirty = false

	m.sd.mode = sdConfirmReset
	m.sd.handleConfirmReset(tea.KeyMsg{Type: tea.KeyEnter})

	if m.sd.mode != sdBrowse {
		t.Fatalf("mode after confirming reset = %v, want sdBrowse", m.sd.mode)
	}
	if m.sd.working != (config.SerialPrefs{}) {
		t.Errorf("working after reset = %+v, want zero value", m.sd.working)
	}
	if !m.sd.dirty {
		t.Error("reset should mark Serial Defaults dirty (not yet saved)")
	}
	// Reset must not touch unrelated config (task rule #14).
	if m.sd.autoReconn != false {
		t.Error("reset should not touch Auto Reconnect")
	}
	eff := m.sd.effective()
	want := serial.DefaultConfig()
	if eff.Baud != want.Baud || eff.FlowControl != want.FlowControl || eff.StopBits != want.StopBits {
		t.Errorf("effective config after reset = %+v, want built-in default %+v", eff, want)
	}
}

func TestSerialDefaultsResetCancel(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.working = config.SerialPrefs{Baud: 921600}
	m.sd.mode = sdConfirmReset

	m.sd.handleConfirmReset(tea.KeyMsg{Type: tea.KeyEsc})
	if m.sd.mode != sdBrowse {
		t.Fatalf("mode after canceling reset = %v, want sdBrowse", m.sd.mode)
	}
	if m.sd.working.Baud != 921600 {
		t.Error("canceling reset should leave the working copy untouched")
	}
}

// TestSerialDefaultsHandleKeyIfEditingDoesNotLeakAcrossScreens confirms the
// funnel-first intercept only fires while Config/Serial Defaults is actually
// on screen — the same guarantee designer/tx/saved's handleKeyIfEditing give
// (see model.handleKey), so leaving a picker open and switching tabs can't
// hijack another screen's keys.
func TestSerialDefaultsHandleKeyIfEditingDoesNotLeakAcrossScreens(t *testing.T) {
	m := enterSerialDefaults(t)
	m.sd.openPicker(sfBaud)
	m.tab = tabMonitor // leave Config without closing the picker
	if _, handled := m.sd.handleKeyIfEditing(m, tea.KeyMsg{Type: tea.KeyEnter}); handled {
		t.Error("handleKeyIfEditing should not intercept keys once the Config tab is no longer active")
	}
}
