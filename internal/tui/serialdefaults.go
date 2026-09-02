package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/device"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// serialField indexes the rows of the Serial Defaults screen — the same
// row-index-as-navigation-unit idiom the Designer uses for its field list
// (see designerState.cursor/rowCount), not a new dispatch shape.
type serialField int

const (
	sfBaud serialField = iota
	sfDataBits
	sfParity
	sfStopBits
	sfFlowControl
	sfAutoReconnect
	sfFieldCount
)

var serialFieldLabels = [sfFieldCount]string{
	sfBaud:          "Baud",
	sfDataBits:      "Data bits",
	sfParity:        "Parity",
	sfStopBits:      "Stop bits",
	sfFlowControl:   "Flow control",
	sfAutoReconnect: "Auto reconnect",
}

// serialDefaultsMode distinguishes the row list (browse) from an open
// picker/form/confirm — the same shape as designerMode.
type serialDefaultsMode int

const (
	sdBrowse serialDefaultsMode = iota
	sdPicker
	sdBaudCustom
	sdConfirmReset
)

// The value/label tables below are the only place Serial Defaults' pickers
// decide what's offered. Every list here is restricted to values the real
// transport actually honors (internal/serial/port.go's
// toLibParity/toLibStopBits/applyFlowControl) — never a value that would be
// a silent no-op once connected (task rule: "do not add UI options the
// backend cannot honor"). In particular FlowXonXoff is deliberately
// excluded: serial.Config.Validate accepts it and the type models it, but
// applyFlowControl does nothing for it (go.bug.st/serial has no XON/XOFF
// Mode field) — see ARCHITECTURE.md "Known limitations". Baud has no fixed
// enum (Config.Baud is a plain int) — serial.BaudPresets are offered as
// quick picks, with an always-present "Custom…" row for any other valid
// value.
var (
	sdDataBitsValues = []int{5, 6, 7, 8}

	sdParityValues = []serial.Parity{serial.ParityNone, serial.ParityEven, serial.ParityOdd, serial.ParityMark, serial.ParitySpace}
	sdParityLabels = []string{"None", "Even", "Odd", "Mark", "Space"}

	sdStopBitsValues = []serial.StopBits{serial.StopBits1, serial.StopBits1_5, serial.StopBits2}
	sdStopBitsLabels = []string{"1", "1.5", "2"}

	sdFlowValues = []serial.FlowControl{serial.FlowNone, serial.FlowRTSCTS}
	sdFlowLabels = []string{"None", "RTS/CTS"}
)

// serialDefaultsState is the Config tab's "Serial Defaults" subsection: an
// editable, explicitly-saved view onto config.App.Serial (tier 3 of
// internal/device.ResolveSerialConfig's four-tier precedence) and
// config.App.Reconnect.Enabled — never a second serial-config
// representation, see ARCHITECTURE.md "Serial Defaults".
type serialDefaultsState struct {
	cursor serialField
	mode   serialDefaultsMode

	working    config.SerialPrefs // edited-but-maybe-unsaved copy of app.Serial
	autoReconn bool               // edited-but-maybe-unsaved copy of app.Reconnect.Enabled
	dirty      bool
	err        string // set on a rejected (invalid) save; cleared on the next successful one

	pickerCursor int
	baudInput    *textForm
}

func newSerialDefaultsState(app config.App) serialDefaultsState {
	return serialDefaultsState{working: app.Serial, autoReconn: app.Reconnect.Enabled}
}

// effective resolves s.working the same way a real connection would (tiers
// 3+4 only — no profile, no override), so the screen always displays
// concrete values, never a blank "unset" field, exactly matching how a
// zero-valued SerialPrefs already behaves for every other caller of
// ResolveSerialConfig.
func (s *serialDefaultsState) effective() serial.Config {
	return device.ResolveSerialConfig(config.App{Serial: s.working}, nil, nil)
}

// handleKeyIfEditing intercepts keys for every Serial Defaults sub-mode. It
// only activates while the Config/Serial Defaults subview is actually on
// screen and a picker/form/confirm is open — the same funnel-first pattern
// designer/tx/saved use (see model.handleKey) so, e.g., typing "921600"
// into the custom-baud field can never be mistaken for the "9"/"2"/"1"
// tab-jump shortcuts.
func (s *serialDefaultsState) handleKeyIfEditing(m *model, msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.tab != tabConfig || m.cfgSection != cfgSerialDefaults || s.mode == sdBrowse {
		return nil, false
	}
	switch s.mode {
	case sdPicker:
		return nil, s.handlePicker(msg)
	case sdBaudCustom:
		return nil, s.handleBaudCustom(msg)
	case sdConfirmReset:
		return nil, s.handleConfirmReset(msg)
	}
	return nil, false
}

// --- browse mode: row navigation + row actions ------------------------------

func (m *model) updateSerialDefaultsBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.sd
	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < sfFieldCount-1 {
			s.cursor++
		}
	case "enter":
		s.openPicker(s.cursor)
	case " ":
		if s.cursor == sfAutoReconnect {
			s.openPicker(s.cursor)
		}
	case "s":
		m.saveSerialDefaults()
	case "r":
		s.mode = sdConfirmReset
	}
	return m, nil
}

// saveSerialDefaults validates the working copy through the exact same
// device.ResolveSerialConfig + serial.Config.Validate() path every other
// caller uses (no new validation logic), then persists via config.SaveApp —
// the existing atomic-write path, no new persistence mechanism.
func (m *model) saveSerialDefaults() {
	s := &m.sd
	candidate := config.App{Serial: s.working}
	if err := device.ResolveSerialConfig(candidate, nil, nil).Validate(); err != nil {
		s.err = err.Error()
		return
	}
	m.app.Serial = s.working
	m.app.Reconnect.Enabled = s.autoReconn
	if err := config.SaveApp(m.cfg.ConfigDir, m.app); err != nil {
		s.err = "save: " + err.Error()
		return
	}
	s.err = ""
	s.dirty = false
	m.status = "saved " + m.cfg.ConfigDir + "/app.yaml"
}

// --- picker: Baud / Data bits / Parity / Stop bits / Flow control ----------

// openPicker opens the row's picker — except Auto Reconnect, which has only
// two states and toggles in place instead of opening a one-item-wide list.
func (s *serialDefaultsState) openPicker(field serialField) {
	if field == sfAutoReconnect {
		s.autoReconn = !s.autoReconn
		s.dirty = true
		return
	}
	s.cursor = field
	s.mode = sdPicker
	s.pickerCursor = 0
	eff := s.effective()
	switch field {
	case sfBaud:
		for i, b := range serial.BaudPresets {
			if b == eff.Baud {
				s.pickerCursor = i
				break
			}
		}
	case sfDataBits:
		for i, d := range sdDataBitsValues {
			if d == eff.DataBits {
				s.pickerCursor = i
				break
			}
		}
	case sfParity:
		for i, p := range sdParityValues {
			if p == eff.Parity {
				s.pickerCursor = i
				break
			}
		}
	case sfStopBits:
		for i, sb := range sdStopBitsValues {
			if sb == eff.StopBits {
				s.pickerCursor = i
				break
			}
		}
	case sfFlowControl:
		for i, f := range sdFlowValues {
			if f == eff.FlowControl {
				s.pickerCursor = i
				break
			}
		}
	}
}

// pickerLabels is the display list for whichever row's picker is open —
// Baud's list always ends with a "Custom…" row for an arbitrary valid rate.
func (s *serialDefaultsState) pickerLabels() []string {
	switch s.cursor {
	case sfBaud:
		labels := make([]string, 0, len(serial.BaudPresets)+1)
		for _, b := range serial.BaudPresets {
			labels = append(labels, strconv.Itoa(b))
		}
		return append(labels, "Custom…")
	case sfDataBits:
		labels := make([]string, len(sdDataBitsValues))
		for i, d := range sdDataBitsValues {
			labels[i] = strconv.Itoa(d)
		}
		return labels
	case sfParity:
		return sdParityLabels
	case sfStopBits:
		return sdStopBitsLabels
	case sfFlowControl:
		return sdFlowLabels
	}
	return nil
}

func (s *serialDefaultsState) handlePicker(msg tea.KeyMsg) bool {
	n := len(s.pickerLabels())
	switch msg.String() {
	case "esc":
		s.mode = sdBrowse
	case "up", "k":
		if s.pickerCursor > 0 {
			s.pickerCursor--
		}
	case "down", "j":
		if s.pickerCursor < n-1 {
			s.pickerCursor++
		}
	case "enter":
		s.confirmPicker()
	}
	return true
}

// confirmPicker applies the selected row of whichever picker is open.
// Baud's trailing "Custom…" row opens a text form instead of applying
// directly.
func (s *serialDefaultsState) confirmPicker() {
	switch s.cursor {
	case sfBaud:
		if s.pickerCursor < len(serial.BaudPresets) {
			s.working.Baud = serial.BaudPresets[s.pickerCursor]
			s.dirty = true
			s.mode = sdBrowse
			return
		}
		s.baudInput = newTextForm([]string{"Baud"}, strconv.Itoa(s.effective().Baud))
		s.baudInput.markDecimal(0)
		s.mode = sdBaudCustom
	case sfDataBits:
		s.working.DataBits = sdDataBitsValues[s.pickerCursor]
		s.dirty = true
		s.mode = sdBrowse
	case sfParity:
		s.working.Parity = string(sdParityValues[s.pickerCursor])
		s.dirty = true
		s.mode = sdBrowse
	case sfStopBits:
		s.working.StopBits = string(sdStopBitsValues[s.pickerCursor])
		s.dirty = true
		s.mode = sdBrowse
	case sfFlowControl:
		s.working.FlowControl = string(sdFlowValues[s.pickerCursor])
		s.dirty = true
		s.mode = sdBrowse
	}
}

// --- custom baud text entry (reuses textForm — see savedpackets.go) --------

func (s *serialDefaultsState) handleBaudCustom(msg tea.KeyMsg) bool {
	submit, cancel := s.baudInput.handleKey(msg)
	if cancel {
		s.baudInput = nil
		s.mode = sdBrowse
		return true
	}
	if submit {
		n, err := strconv.Atoi(strings.TrimSpace(s.baudInput.values[0]))
		if err != nil || n <= 0 {
			s.baudInput.message = "baud must be a positive integer"
			return true
		}
		s.working.Baud = n
		s.dirty = true
		s.baudInput = nil
		s.mode = sdBrowse
	}
	return true
}

// --- reset to built-in defaults ---------------------------------------------

// handleConfirmReset zeroes only the five UART fields (Baud/DataBits/
// Parity/StopBits/FlowControl) back to "unset" — which already falls
// through internal/device.ResolveSerialConfig's tier 3/4 to
// serial.DefaultConfig() (115200 8N1, no flow control), no new default
// constants introduced. Auto Reconnect is deliberately untouched: the
// task's own reset scope is the UART framing fields only, and resetting
// unrelated config isn't appropriate here.
func (s *serialDefaultsState) handleConfirmReset(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "y", "enter":
		s.working = config.SerialPrefs{}
		s.dirty = true
		s.mode = sdBrowse
	case "esc", "n":
		s.mode = sdBrowse
	}
	return true
}

// --- rendering ---------------------------------------------------------------

func parityLabel(p serial.Parity) string {
	for i, v := range sdParityValues {
		if v == p {
			return sdParityLabels[i]
		}
	}
	return string(p)
}

func flowLabel(f serial.FlowControl) string {
	for i, v := range sdFlowValues {
		if v == f {
			return sdFlowLabels[i]
		}
	}
	return string(f) // e.g. a hand-edited "xon_xoff" in app.yaml — show it, never hide it
}

func (m *model) viewSerialDefaults() string {
	s := &m.sd
	switch s.mode {
	case sdPicker:
		return m.viewSerialPicker()
	case sdBaudCustom:
		return s.baudInput.view("Custom baud")
	case sdConfirmReset:
		return accentBox.Render(sectionStyle.Render("Reset Serial Defaults?") + "\n\n" +
			dimStyle.Render("Restores 115200 8N1, no flow control.") + "\n\n" +
			renderHints(hint("y/enter", "confirm"), hint("esc/n", "cancel")))
	}

	eff := s.effective()
	title := "Serial Defaults"
	if s.dirty {
		title += " " + warnStyle.Render("*")
	}
	var b strings.Builder
	b.WriteString(sectionStyle.Render(title) + "\n\n")

	row := func(i serialField, value string) {
		marker := "  "
		style := fieldTextStyle
		if i == s.cursor {
			marker = keyStyle.Render("▸ ")
			style = keyStyle
		}
		b.WriteString(fmt.Sprintf("%s%-14s %s\n", marker, serialFieldLabels[i], style.Render(value)))
	}
	row(sfBaud, strconv.Itoa(eff.Baud))
	row(sfDataBits, strconv.Itoa(eff.DataBits))
	row(sfParity, parityLabel(eff.Parity))
	row(sfStopBits, string(eff.StopBits))
	row(sfFlowControl, flowLabel(eff.FlowControl))
	reconnLabel := "Off"
	if s.autoReconn {
		reconnLabel = "On"
	}
	row(sfAutoReconnect, reconnLabel)

	if s.err != "" {
		b.WriteString("\n" + badStyle.Render(s.err) + "\n")
	}
	b.WriteString("\n" + renderHints(
		hint("↑/↓", "select"), hint("enter", "edit"),
		KeyHint{Key: "s", Desc: "save", Disabled: !s.dirty},
		hint("r", "reset")))
	return boxStyle.Width(m.diagramWidth()).Render(b.String())
}

func (m *model) viewSerialPicker() string {
	s := &m.sd
	var b strings.Builder
	b.WriteString(sectionStyle.Render(serialFieldLabels[s.cursor]) + "\n\n")
	for i, label := range s.pickerLabels() {
		marker := "  "
		if i == s.pickerCursor {
			marker = keyStyle.Render("▸ ")
		}
		b.WriteString(marker + label + "\n")
	}
	b.WriteString("\n" + renderHints(hint("enter", "select"), hint("esc", "cancel")))
	return accentBox.Render(b.String())
}
