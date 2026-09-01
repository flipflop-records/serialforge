package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/config"
)

// updateConfig toggles the small set of UI preferences persisted to
// app.yaml. This is deliberately minimal — reconnect
// timing/backoff and other deeper settings are file-editable
// (~/.config/serialforge/app.yaml, or $SERIALFORGE_CONFIG_DIR) but don't yet have a
// dedicated TUI editor; see ARCHITECTURE.md "Remaining work".
func (m *model) updateConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "t":
		if m.app.UI.ShowTimestamps {
			m.app.UI.ShowTimestamps = false
		} else {
			m.app.UI.ShowTimestamps = true
		}
	case "r":
		m.app.Reconnect.Enabled = !m.app.Reconnect.Enabled
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
	body := fmt.Sprintf(
		"%s\n\n"+
			"  %-24s %s\n"+
			"  %-24s %s\n"+
			"  %-24s %v\n"+
			"  %-24s %v\n\n"+
			"%s\n",
		sectionStyle.Render("Configuration"),
		"Config directory", m.cfg.ConfigDir,
		"Version", m.cfg.Version,
		dimStyle.Render("Show timestamps [t]"), m.app.UI.ShowTimestamps,
		dimStyle.Render("Auto-reconnect [r]"), m.app.Reconnect.Enabled,
		dimStyle.Render("s save   protocols: `serialforge protocol …`   devices: `serialforge device …`"),
	)
	return body
}
