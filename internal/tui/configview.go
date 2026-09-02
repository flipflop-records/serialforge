package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/config"
)

// Config tab sections — General (small UI preferences) and Serial Defaults
// (internal/config.App.Serial + Reconnect.Enabled, see serialdefaults.go),
// switched with '[' / ']' — the same contextual-subview idiom the Packets
// tab uses for Designer/TX Builder/RX Inspector/Saved, not a new pattern.
const (
	cfgGeneral = iota
	cfgSerialDefaults
	cfgSectionCount
)

var cfgSectionNames = []string{"General", "Serial Defaults"}

// updateConfig dispatches '[' / ']' section switching, then routes to the
// active section's own handler. Serial Defaults' own sub-modes (picker/
// custom-baud/confirm-reset) are intercepted earlier, in model.handleKey via
// serialDefaultsState.handleKeyIfEditing — by the time a key reaches here,
// Serial Defaults is always in its plain row-browse mode.
func (m *model) updateConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "[":
		m.cfgSection = (m.cfgSection - 1 + cfgSectionCount) % cfgSectionCount
		return m, nil
	case "]":
		m.cfgSection = (m.cfgSection + 1) % cfgSectionCount
		return m, nil
	}
	switch m.cfgSection {
	case cfgGeneral:
		return m.updateConfigGeneral(msg)
	case cfgSerialDefaults:
		return m.updateSerialDefaultsBrowse(msg)
	}
	return m, nil
}

// updateConfigGeneral toggles the small set of purely cosmetic UI
// preferences persisted to app.yaml. Deeper settings that used to be
// file-editable-only now have their own section — see Serial Defaults.
func (m *model) updateConfigGeneral(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "t":
		m.app.UI.ShowTimestamps = !m.app.UI.ShowTimestamps
	case "s":
		if err := config.SaveApp(m.cfg.ConfigDir, m.app); err != nil {
			m.status = "save: " + err.Error()
		} else {
			m.status = "saved " + m.cfg.ConfigDir + "/app.yaml"
		}
	}
	return m, nil
}

func (m *model) viewConfig() string {
	subtabs := ""
	for i, n := range cfgSectionNames {
		if i == m.cfgSection {
			subtabs += activeStyle.Render("• "+n) + "  "
		} else {
			subtabs += dimStyle.Render("○ "+n) + "  "
		}
	}
	subtabs += renderHints(hint("[/]", "switch"))

	var body string
	switch m.cfgSection {
	case cfgGeneral:
		body = m.viewConfigGeneral()
	case cfgSerialDefaults:
		body = m.viewSerialDefaults()
	}
	return subtabs + "\n\n" + body
}

func (m *model) viewConfigGeneral() string {
	timestamps := "Off"
	if m.app.UI.ShowTimestamps {
		timestamps = "On"
	}
	return fmt.Sprintf(
		"%s\n\n"+
			"  %-24s %s\n"+
			"  %-24s %s\n"+
			"  %-24s %s\n\n"+
			"%s\n",
		sectionStyle.Render("General"),
		"Config directory", m.cfg.ConfigDir,
		"Version", m.cfg.Version,
		"Show timestamps", timestamps,
		renderHints(hint("t", "toggle timestamps"), hint("s", "save"))+"\n"+
			dimStyle.Render("protocols: `serialforge protocol …`   devices: `serialforge device …`"),
	)
}
