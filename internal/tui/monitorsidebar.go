package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

// Monitor's Saved Packets sidebar — a VIEW/controller surface over existing
// domain state, not a parallel model. It owns exactly two pieces of its own
// state (cursor, scroll position); everything it displays is read fresh
// from savedpacket.Store + the active protocol on every render/keypress —
// see filteredSavedPackets and (*monitorSidebarState).selected. Sending
// goes through the exact same sendSavedPacket the dedicated Saved Packets
// screen and hotkey dispatch already use (see updateMonitorSaved) — there
// is no Monitor-specific packet-building or serialization path. See
// ARCHITECTURE.md "Monitor: Saved Packets sidebar" for the full rationale.

// monitorPane is which of Monitor's two panes currently has focus.
type monitorPane int

const (
	monitorPaneTraffic monitorPane = iota
	monitorPaneSaved
)

// Sizing constants the sidebar's visibility/width are derived from —
// concrete floors based on what the content actually needs to render
// legibly, not arbitrary numbers. monitorSidebarMinWidth/
// monitorTrafficMinWidth/monitorSidebarMaxWidth are all boxStyle.Width()
// *argument* values (lipgloss.Width(N) already bakes boxStyle's own
// Padding(0, 1) into N — verified directly: boxStyle.Width(10).Render("X")
// produces a box whose bordered rows are 12 columns wide, with exactly 10
// columns between the border characters, so a box's usable *text* width is
// its Width() argument minus 2 (padding), and its on-screen footprint is
// its Width() argument plus 2 (border) — never assumed without checking).
//   - monitorSidebarMinWidth: a reasonably-named packet (~18 chars) plus a
//     hotkey column, as a Width() argument (i.e. already includes the 2
//     columns Padding(0,1) will consume from it).
//   - monitorTrafficMinWidth: a timestamp ("15:04:05.000", 12 cols) plus
//     "RX"/"TX" plus enough hex bytes for the traffic view to still be
//     useful, not squeezed to near-nothing.
//   - monitorSidebarMaxWidth: caps how much of a very wide terminal the
//     sidebar claims — it stays a sidebar, never competes with the
//     traffic pane for primary space.
//   - monitorBoxOverhead: the on-screen footprint a box's rounded border
//     adds *beyond* its Width() argument — 2 columns (1 each side); the
//     padding is already inside Width() per the note above, so this is not
//     also +2 for padding. Two panes side by side each pay this once.
const (
	monitorSidebarMinWidth = 28
	monitorSidebarMaxWidth = 40
	monitorTrafficMinWidth = 50
	monitorPaneGap         = 2
	monitorBoxOverhead     = 2

	// monitorDiagramMinWidth is the sidebar Width() value above which the
	// selected-packet detail also renders the register-style diagram
	// (RenderDiagram already wraps narrow widths reasonably — see
	// Designer's own narrow-width tests — but a diagram crammed under
	// monitorSidebarMinWidth's floor reads as noise, not detail).
	monitorDiagramMinWidth = 34
)

// monitorSidebarVisible reports whether the terminal is wide enough to show
// the sidebar alongside a still-useful traffic pane — both panes' own
// rendered (border+padding-inclusive) width, plus the gap between them,
// must fit. Below this, Monitor falls back to the existing full-width
// traffic view — the dedicated Packets → Saved screen and packet hotkeys
// remain fully available either way (see ARCHITECTURE.md "Hotkey policy").
func (m *model) monitorSidebarVisible() bool {
	need := monitorTrafficMinWidth + monitorBoxOverhead +
		monitorPaneGap +
		monitorSidebarMinWidth + monitorBoxOverhead
	return m.width >= need
}

// monitorSidebarWidth is the sidebar's actual rendered *content* width
// (before boxStyle's own border/padding) once it's visible — floored at
// monitorSidebarMinWidth, capped at monitorSidebarMaxWidth, and otherwise a
// modest share of whatever's left over both panes' floors and overhead, so
// a very wide terminal gives the sidebar a little more room without ever
// letting it dominate.
func (m *model) monitorSidebarWidth() int {
	used := monitorTrafficMinWidth + monitorBoxOverhead +
		monitorPaneGap +
		monitorSidebarMinWidth + monitorBoxOverhead
	extra := m.width - used
	w := monitorSidebarMinWidth
	if extra > 0 {
		w += extra / 3
	}
	if w > monitorSidebarMaxWidth {
		w = monitorSidebarMaxWidth
	}
	return w
}

// monitorSidebarState is the sidebar's own UI state — a selection cursor
// and scroll offset into whatever filteredSavedPackets currently returns.
// Deliberately not a copy of any Saved Packet data.
type monitorSidebarState struct {
	cursor int
	scroll int
}

// filteredSavedPackets returns the Saved Packets belonging to the currently
// active protocol (m.activeSchema), in the Store's own order — the same
// order the dedicated Saved Packets screen presents (product requirement:
// one logical ordering, not an independent sort here). Recomputed fresh on
// every call, never cached on the model, so a rename/hotkey change/delete/
// create/protocol-reference edit made anywhere else is picked up
// immediately the next time Monitor renders or a key is handled — exactly
// the same "read the store fresh" pattern savedState.selected already uses.
// nil (not an error) when there's no active protocol.
func (m *model) filteredSavedPackets() []savedpacket.SavedPacket {
	if m.activeSchema == nil {
		return nil
	}
	var out []savedpacket.SavedPacket
	for _, sp := range m.cfg.SavedPackets.All() {
		if sp.Protocol == m.activeSchema.Name {
			out = append(out, sp)
		}
	}
	return out
}

// selected returns the Saved Packet currently under the sidebar's cursor,
// against the current filtered list — mirrors savedState.selected.
func (s *monitorSidebarState) selected(m *model) (savedpacket.SavedPacket, bool) {
	list := m.filteredSavedPackets()
	if s.cursor < 0 || s.cursor >= len(list) {
		return savedpacket.SavedPacket{}, false
	}
	return list[s.cursor], true
}

// clamp repositions cursor/scroll to stay valid against n (the current
// filtered list's length) — called before every read or key-handling pass,
// so a packet disappearing (deleted, or its Protocol reference changed
// away) or the active protocol changing out from under the sidebar can
// never leave the cursor pointing past the end of the list it's now
// looking at.
func (s *monitorSidebarState) clamp(n int) {
	if n <= 0 {
		s.cursor, s.scroll = 0, 0
		return
	}
	if s.cursor >= n {
		s.cursor = n - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.scroll > s.cursor {
		s.scroll = s.cursor
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
}

// visibleWindow returns the [start,end) slice bounds to render given rows
// of vertical space, adjusting scroll (never cursor) so the selected row
// always stays on screen — independent of and never affecting the traffic
// pane's own rendering, which reads m.events/m.height entirely separately.
func (s *monitorSidebarState) visibleWindow(total, rows int) (start, end int) {
	if rows <= 0 || total <= 0 {
		return 0, 0
	}
	if rows > total {
		rows = total
	}
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
	if s.cursor >= s.scroll+rows {
		s.scroll = s.cursor - rows + 1
	}
	if s.scroll > total-rows {
		s.scroll = total - rows
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
	start = s.scroll
	end = start + rows
	if end > total {
		end = total
	}
	return start, end
}

// --- update -----------------------------------------------------------------

// updateMonitorSaved handles keys while the sidebar has focus: select and
// send. Enter goes straight through sendSavedPacket — the exact function
// the dedicated Saved Packets screen's direct-send and the global hotkey
// dispatch both call — so validation, AUTO CRC recalculation, the
// not-connected/incompatible status wording, and the actual bytes put on
// the wire are all identical to every other way of sending a Saved Packet
// in the app. No second build/send path.
func (m *model) updateMonitorSaved(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.monitorSaved
	list := m.filteredSavedPackets()
	s.clamp(len(list))
	switch msg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(list)-1 {
			s.cursor++
		}
	case "enter":
		if sp, ok := s.selected(m); ok {
			m.sendSavedPacket(sp, "")
		}
	}
	return m, nil
}

// --- rendering ---------------------------------------------------------------

// viewMonitorSidebar renders the sidebar pane as one focus-colored bordered
// box — the list, and (height/width permitting) the selected packet's
// detail, in that same box, one blank line apart. Deliberately one box, not
// list-box-plus-detail-box stacked: that would need the two boxes' own
// border overhead to add up to exactly the traffic pane's height for the
// side-by-side layout to align, which is exactly the kind of TUI-side
// arithmetic this package avoids duplicating when a simpler structure does
// the same job — one Height(paneHeight) call, matching the traffic box's
// own, is enough.
func (m *model) viewMonitorSidebar(paneHeight int) string {
	s := &m.monitorSaved
	list := m.filteredSavedPackets()
	s.clamp(len(list))
	width := m.monitorSidebarWidth()

	border := normalBorder
	if m.monitorFocus == monitorPaneSaved {
		border = selectedBorder
	}

	var b strings.Builder
	b.WriteString(sectionStyle.Render("Saved Packets") + "\n\n")
	contentRows := paneHeight - 2 // title + blank line already spent
	if contentRows < 1 {
		contentRows = 1
	}

	switch {
	case m.activeSchema == nil:
		b.WriteString(secondaryStyle.Render("No active protocol"))
	case len(list) == 0:
		b.WriteString(secondaryStyle.Render("No saved packets for " + m.activeSchema.Name))
	default:
		sp, hasSelection := s.selected(m)
		showDetail := hasSelection && width >= monitorDiagramMinWidth && contentRows >= 14

		listRows := contentRows
		detailRows := 0
		if showDetail {
			detailRows = 9
			if max := contentRows - 4; detailRows > max {
				detailRows = max
			}
			listRows = contentRows - detailRows - 1 // 1 row for the blank separator
		}
		if listRows < 1 {
			listRows = 1
		}

		start, end := s.visibleWindow(len(list), listRows)
		for i := start; i < end; i++ {
			b.WriteString(monitorSavedRow(m, list[i], i == s.cursor, width-2) + "\n") // -2: boxStyle's own padding
		}

		if showDetail && detailRows > 0 {
			b.WriteString("\n" + m.viewSavedDetail(sp, width-2))
		}
	}

	box := boxStyle.Width(width).Height(paneHeight).BorderForeground(border)
	return box.Render(strings.TrimRight(b.String(), "\n"))
}

// monitorSavedRow renders one sidebar list row: selection marker, the name
// (truncated to fit), an incompatible-packet mark (same "!" warnStyle glyph
// and Resolve check the dedicated Saved Packets screen already uses — see
// viewSaved), and the hotkey using the exact keyStyle/dimStyle split every
// other hotkey display in the app already uses (never an empty placeholder
// that looks like an assigned key). rowWidth is the row's full available
// width (the sidebar's content width); the name column is padded on the
// *plain* string before any styling is applied — padding an already-styled
// string would pad against its ANSI-escape-inclusive byte length, not its
// visible width, silently breaking alignment the moment colors are on.
func monitorSavedRow(m *model, sp savedpacket.SavedPacket, isSelected bool, rowWidth int) string {
	marker := "  "
	nameStyle := primaryStyle
	if isSelected {
		marker = keyStyle.Render("▸ ")
		nameStyle = keyStyle
	}
	broken := false
	if res := sp.Resolve(m.cfg.Protocols); res.Status != savedpacket.StatusOK {
		broken = true
	}
	hotkey := disabledStyle.Render("·")
	if sp.Hotkey != "" {
		hotkey = keyStyle.Render(sp.Hotkey)
	}

	// Fixed-width trailing columns: a 1-char mark column + 1 gap + 1-char
	// hotkey column, so every row lines up regardless of whether this
	// particular packet is broken or has a hotkey.
	const trailingCols = 3
	nameWidth := rowWidth - len(marker) - trailingCols
	if nameWidth < 3 {
		nameWidth = 3
	}
	name := sp.Name
	if len(name) > nameWidth {
		if nameWidth > 1 {
			name = name[:nameWidth-1] + "…"
		} else {
			name = name[:nameWidth]
		}
	}
	name = fmt.Sprintf("%-*s", nameWidth, name) // pad the plain string first

	mark := " "
	if broken {
		mark = warnStyle.Render("!")
	}
	return marker + nameStyle.Render(name) + mark + " " + hotkey
}
