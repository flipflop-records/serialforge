package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/device"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// addDeviceForm is a small sequential text-entry form (alias/path/VID/PID/
// serial number/baud), the same "one field at a time, Tab/Enter to
// advance" interaction used by every other form in this TUI (the manual
// connect form, the CRC custom-parameter form, etc.) — kept consistent
// rather than inventing a second input idiom. Path is
// optional: a profile identified only by USB metadata (VID/PID/serial
// number) still resolves normally, and a profile that sets ONLY a path
// (skip VID/PID entirely) is exactly how a manual/virtual serial path — a
// socat PTY, a device the platform's enumerator doesn't recognize — gets a
// saved, reusable alias; see device.Resolve's doc comment. Neither is
// required together; nothing here forces VID/PID when a path is given.
type addDeviceForm struct {
	labels []string
	values []string
	cursor int
}

const (
	addFieldAlias = iota
	addFieldPath
	addFieldVID
	addFieldPID
	addFieldSerial
	addFieldBaud
)

func newAddDeviceForm(prefill *serial.PortInfo) *addDeviceForm {
	f := &addDeviceForm{labels: []string{"Alias", "Path (manual, optional)", "VID", "PID", "Serial number", "Baud"}}
	f.values = make([]string, len(f.labels))
	f.values[addFieldBaud] = "115200"
	if prefill != nil {
		f.values[addFieldPath] = prefill.Path
		f.values[addFieldVID] = prefill.VID
		f.values[addFieldPID] = prefill.PID
		f.values[addFieldSerial] = prefill.SerialNumber
	}
	return f
}

func (m *model) devAddHandleKeyIfEditing(msg tea.KeyMsg) (tea.Cmd, bool) {
	f := m.devAdd
	// Gated on m.tab too (not just f == nil), matching txState/savedState's
	// own handleKeyIfEditing — defense in depth so this modal can never
	// keep swallowing keys (including q/tab) if m.tab ever changed while
	// it was still open. See ARCHITECTURE.md "Key routing priority".
	if f == nil || m.tab != tabDevices {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.devAdd = nil
		return nil, true
	case tea.KeyTab, tea.KeyDown:
		f.cursor = (f.cursor + 1) % len(f.labels)
		return nil, true
	case tea.KeyShiftTab, tea.KeyUp:
		f.cursor = (f.cursor - 1 + len(f.labels)) % len(f.labels)
		return nil, true
	case tea.KeyBackspace:
		v := f.values[f.cursor]
		if len(v) > 0 {
			f.values[f.cursor] = v[:len(v)-1]
		}
		return nil, true
	case tea.KeyEnter:
		if f.cursor < len(f.labels)-1 {
			f.cursor++
			return nil, true
		}
		return m.submitAddDevice(), true
	default:
		if msg.Type == tea.KeyRunes {
			if f.cursor == addFieldBaud {
				// Every other field here (Alias/Path/VID/PID/Serial) is
				// free text on purpose — VID/PID in particular are
				// free-form comparison strings with no declared width,
				// not parsed as numbers (see submitAddDevice) — only Baud
				// is actually numeric.
				f.values[f.cursor] = appendDecimalDigits(f.values[f.cursor], msg.Runes)
			} else {
				f.values[f.cursor] += string(msg.Runes)
			}
		}
		return nil, true
	}
}

func (m *model) submitAddDevice() tea.Cmd {
	f := m.devAdd
	alias := strings.TrimSpace(f.values[addFieldAlias])
	if alias == "" {
		m.status = "alias must not be empty"
		return nil
	}
	baud, err := strconv.Atoi(strings.TrimSpace(f.values[addFieldBaud]))
	if err != nil || baud <= 0 {
		baud = 115200
	}
	p := device.Profile{
		Alias:        alias,
		Path:         strings.TrimSpace(f.values[addFieldPath]),
		VID:          strings.TrimSpace(f.values[addFieldVID]),
		PID:          strings.TrimSpace(f.values[addFieldPID]),
		SerialNumber: strings.TrimSpace(f.values[addFieldSerial]),
		Baud:         baud,
	}
	if err := m.devices.Put(p); err != nil {
		m.status = err.Error()
		return nil
	}
	if err := m.devices.Save(); err != nil {
		m.status = "save: " + err.Error()
		return nil
	}
	m.status = "saved device profile " + alias
	m.devAdd = nil
	m.refreshVirtualCount()
	return nil
}

// manualConnectForm is the fast path for a virtual/development port: type a
// path and a baud rate, connect immediately — no saved profile required.
// 'a' (addDeviceForm) also accepts a manual path, for when you want it to
// persist as a named alias; this is for "just connect, right now".
type manualConnectForm struct {
	path   string
	baud   string
	cursor int // 0 = path, 1 = baud
}

func newManualConnectForm() *manualConnectForm {
	return &manualConnectForm{baud: "115200"}
}

func (m *model) devManualHandleKeyIfEditing(msg tea.KeyMsg) (tea.Cmd, bool) {
	f := m.devManual
	if f == nil || m.tab != tabDevices {
		return nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.devManual = nil
		return nil, true
	case tea.KeyTab, tea.KeyDown, tea.KeyShiftTab, tea.KeyUp:
		f.cursor = 1 - f.cursor
		return nil, true
	case tea.KeyBackspace:
		if f.cursor == 0 {
			if len(f.path) > 0 {
				f.path = f.path[:len(f.path)-1]
			}
		} else if len(f.baud) > 0 {
			f.baud = f.baud[:len(f.baud)-1]
		}
		return nil, true
	case tea.KeyEnter:
		return m.submitManualConnect(), true
	default:
		if msg.Type == tea.KeyRunes {
			if f.cursor == 0 {
				f.path += string(msg.Runes) // a filesystem path — free text
			} else {
				f.baud = appendDecimalDigits(f.baud, msg.Runes)
			}
		}
		return nil, true
	}
}

func (m *model) submitManualConnect() tea.Cmd {
	f := m.devManual
	path := strings.TrimSpace(f.path)
	if path == "" {
		m.status = "enter a path to connect"
		return nil
	}
	var overrideBaud *int
	if baud, err := strconv.Atoi(strings.TrimSpace(f.baud)); err == nil && baud > 0 {
		overrideBaud = &baud
	}
	// No saved profile for an ad hoc manual connect — app-config default
	// and built-in default are still the fallback tiers (same precedence
	// as the CLI's --port; see device.ResolveSerialConfig).
	cfg := device.ResolveSerialConfig(m.app, nil, overrideBaud)
	m.devManual = nil
	return m.connect(path, cfg, m.activeSchema, connectReasonNew)
}

func (m *model) viewManualConnectForm() string {
	f := m.devManual
	row := func(i int, label, value string) string {
		marker := "  "
		style := fieldTextStyle
		if i == f.cursor {
			marker = keyStyle.Render("▸ ")
			style = keyStyle
		}
		return fmt.Sprintf("%s%-8s %s", marker, label, style.Render(value))
	}
	body := fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
		sectionStyle.Render("Manual connect"),
		row(0, "Path", f.path),
		row(1, "Baud", f.baud),
		dimStyle.Render("e.g. /tmp/serialforge-a or /dev/ttys003 (a socat PTY, or any\npath the automatic scan doesn't recognize)")+"\n"+
			renderHints(hint("tab/↓", "next field"), hint("enter", "connect"), hint("esc", "cancel")))
	return accentBox.Render(body)
}

func (m *model) updateDevices(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := len(m.devices.All()) + len(m.detected)
	switch msg.String() {
	case "up", "k":
		if m.devCursor > 0 {
			m.devCursor--
		}
	case "down", "j":
		if m.devCursor < total-1 {
			m.devCursor++
		}
	case "r":
		m.refreshDetected()
		m.status = fmt.Sprintf("rescanned: %d ports found", len(m.detected))
	case "a":
		var prefill *serial.PortInfo
		if idx := m.devCursor - len(m.devices.All()); idx >= 0 && idx < len(m.detected) {
			prefill = &m.detected[idx]
		}
		m.devAdd = newAddDeviceForm(prefill)
	case "m":
		m.devVirtual = buildVirtualChooserFunc(m)
	case "s":
		if m.connectedPath == "" {
			m.status = "not connected — nothing to save"
		} else {
			m.devSave = &saveProfileForm{}
		}
	case "d":
		profiles := m.devices.All()
		if m.devCursor < len(profiles) {
			m.devices.Delete(profiles[m.devCursor].Alias)
			m.devices.Save()
			m.status = "deleted " + profiles[m.devCursor].Alias
			m.refreshVirtualCount()
		}
	case "enter", "c":
		return m, m.connectSelectedDevice()
	}
	return m, nil
}

func (m *model) connectSelectedDevice() tea.Cmd {
	profiles := m.devices.All()
	if m.devCursor < len(profiles) {
		p := profiles[m.devCursor]
		info, err := device.Resolve(p, m.detected)
		if err != nil {
			m.status = err.Error()
			m.logEvent(LogError, "Connect %s failed: %s", p.Alias, err.Error())
			return nil
		}
		return m.connect(info.Path, device.ResolveSerialConfig(m.app, &p, nil), m.activeSchema, connectReasonNew)
	}
	idx := m.devCursor - len(profiles)
	if idx >= 0 && idx < len(m.detected) {
		info := m.detected[idx]
		return m.connect(info.Path, device.ResolveSerialConfig(m.app, nil, nil), m.activeSchema, connectReasonNew)
	}
	return nil
}

func (m *model) viewDevices() string {
	if m.devAdd != nil {
		return m.viewAddDeviceForm()
	}
	if m.devManual != nil {
		return m.viewManualConnectForm()
	}
	if m.devVirtual != nil {
		return m.viewVirtualChooser()
	}
	if m.devSave != nil {
		return m.viewSaveProfileForm()
	}

	var b strings.Builder
	b.WriteString(sectionStyle.Render("Saved profiles") + "\n")
	profiles := m.devices.All()
	if len(profiles) == 0 {
		b.WriteString(dimStyle.Render("  (none yet — press 'a' on a detected port below to save one)") + "\n")
	}
	for i, p := range profiles {
		cursor := "  "
		if i == m.devCursor {
			cursor = keyStyle.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%-16s vid=%s pid=%s baud=%d\n", cursor, p.Alias, p.VID, p.PID, p.SerialConfig().Baud))
	}

	b.WriteString("\n" + sectionStyle.Render("Detected hardware ports") + "\n")
	if m.devDetectErr != "" {
		b.WriteString("  " + badStyle.Render(m.devDetectErr) + "\n")
	} else if len(m.detected) == 0 {
		b.WriteString(dimStyle.Render("  (none found — press 'r' to rescan)") + "\n")
	}
	for i, info := range m.detected {
		idx := len(profiles) + i
		cursor := "  "
		if idx == m.devCursor {
			cursor = keyStyle.Render("▸ ")
		}
		meta := ""
		if info.VID != "" {
			meta = fmt.Sprintf("vid=%s pid=%s", info.VID, info.PID)
		}
		if info.Product != "" {
			meta += " " + info.Product
		}
		b.WriteString(fmt.Sprintf("%s%-28s %s\n", cursor, info.Path, dimStyle.Render(meta)))
	}

	// Deliberately a separate concept, never merged into the list above —
	// see ARCHITECTURE.md "Virtual / manual endpoint discovery". A dedicated
	// chooser ('m'), not an inline entry here, is what makes "typing a
	// path" the fallback rather than the primary workflow.
	b.WriteString("\n" + sectionStyle.Render("Virtual / manual endpoints") + "  " +
		dimStyle.Render(fmt.Sprintf("(%d discovered — press 'm' to browse)", m.virtualCount)) + "\n")

	hints := []KeyHint{
		hint("↑/↓", "select"), hint("enter/c", "connect"), hint("a", "save as profile"),
		hint("m", "virtual/manual"), hint("d", "delete"), hint("r", "rescan"),
	}
	if m.connectedPath != "" {
		hints = append(hints, hint("s", "save connection as profile"))
	}
	b.WriteString("\n" + renderHints(hints...))
	return b.String()
}

func (m *model) viewAddDeviceForm() string {
	var b strings.Builder
	b.WriteString(sectionStyle.Render("New device profile") + "\n\n")
	for i, label := range m.devAdd.labels {
		marker := "  "
		style := fieldTextStyle
		if i == m.devAdd.cursor {
			marker = keyStyle.Render("▸ ")
			style = keyStyle
		}
		b.WriteString(fmt.Sprintf("%s%-14s %s\n", marker, label, style.Render(m.devAdd.values[i])))
	}
	b.WriteString("\n" + renderHints(hint("tab/↓", "next field"), hint("enter", "confirm (last field saves)"), hint("esc", "cancel")))
	return accentBox.Render(b.String())
}
