package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// updatePackets dispatches keys for the Packets tab: '[' / ']' switch
// between the Designer / TX Builder / RX Inspector subviews (contextual
// subviews within one tab, per product spec §28 — "do not create
// unnecessary tab explosions"); everything else goes to the active
// subview's own handler.
func (m *model) updatePackets(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "[":
		m.packetsView = (m.packetsView - 1 + packetsViewCount) % packetsViewCount
		return m, nil
	case "]":
		m.packetsView = (m.packetsView + 1) % packetsViewCount
		return m, nil
	}
	switch m.packetsView {
	case packetsDesigner:
		return m.updateDesigner(msg)
	case packetsTX:
		return m.updateTX(msg)
	case packetsRX:
		return m.updateRX(msg)
	case packetsSaved:
		return m.updateSaved(msg)
	}
	return m, nil
}

func (m *model) viewPackets() string {
	subtabs := ""
	for i, n := range packetsViewNames {
		if i == m.packetsView {
			subtabs += activeStyle.Render("• "+n) + "  "
		} else {
			subtabs += dimStyle.Render("○ "+n) + "  "
		}
	}
	subtabs += renderHints(hint("[/]", "switch"))

	var body string
	switch m.packetsView {
	case packetsDesigner:
		body = m.viewDesigner()
	case packetsTX:
		body = m.viewTX()
	case packetsRX:
		body = m.viewRX()
	case packetsSaved:
		body = m.viewSaved()
	}
	return subtabs + "\n\n" + body
}
