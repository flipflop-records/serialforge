package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

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

// monitorPaneName is monitorFocus's debug-log-friendly name.
func monitorPaneName(p monitorPane) string {
	if p == monitorPaneSaved {
		return "Saved"
	}
	return "Traffic"
}

// Sizing constants the sidebar's visibility/width are derived from —
// concrete floors based on what the content actually needs to render
// legibly, not arbitrary numbers. monitorSidebarMinWidth/
// monitorTrafficMinWidth are boxStyle.Width() *argument* values
// (lipgloss.Width(N) already bakes boxStyle's own Padding(0, 1) into N —
// verified directly: boxStyle.Width(10).Render("X") produces a box whose
// bordered rows are 12 columns wide, with exactly 10 columns between the
// border characters, so a box's usable *text* width is its Width()
// argument minus 2 (padding), and its on-screen footprint is its Width()
// argument plus 2 (border) — never assumed without checking).
//   - monitorSidebarMinWidth: a reasonably-named packet (~18 chars) plus a
//     hotkey column, as a Width() argument (i.e. already includes the 2
//     columns Padding(0,1) will consume from it).
//   - monitorTrafficMinWidth: a timestamp ("15:04:05.000", 12 cols) plus
//     "RX"/"TX" plus enough hex bytes for the traffic view to still be
//     useful, not squeezed to near-nothing.
//   - monitorBoxOverhead: the on-screen footprint a box's rounded border
//     adds *beyond* its Width() argument — 2 columns (1 each side); the
//     padding is already inside Width() per the note above, so this is not
//     also +2 for padding. Two panes side by side each pay this once.
//
// There is deliberately no sidebar *maximum* width constant. Before the
// adjustable split, one existed (capped at 40) to keep an entirely
// automatic sidebar from dominating a very wide terminal. Now that the user
// explicitly controls the split (see "adjustable split" below), that cap
// would only get in the way of the feature's whole point — the traffic
// pane's own minimum is the sidebar's real ceiling.
const (
	monitorSidebarMinWidth = 28
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

// --- adjustable split ---------------------------------------------------
//
// Monitor's traffic/Saved-Packets split is user-adjustable (Left/Right while
// the sidebar has focus — see updateMonitorSaved). The user's preference is
// stored as a normalized ratio, not a column count, precisely so it scales
// with terminal width instead of staying pinned to whatever column count
// happened to be true the last time it was set — a 0.40 preference means
// "40% of the splittable width" whether that terminal is 100 or 180 columns
// wide. The ratio lives at m.app.UI.MonitorSavedPacketsRatio (the existing
// application config, persisted through config.SaveApp — no separate
// preference file; see the debounced-save section below) and is read fresh,
// normalized/clamped, on every render — never cached elsewhere on the
// model.
//
// Preferred ratio vs. actual rendered width are deliberately two different
// things (task requirement: a terminal resize must never overwrite the
// user's preference). monitorSidebarWidth() always computes the *actual*
// on-screen width fresh from the *current* m.width and the *stored* ratio,
// clamping to both panes' minimums — it never writes back to the stored
// ratio. Only a deliberate Left/Right keypress (or Reset) changes the
// stored preference. So: terminal resize -> recompute actual widths from
// the unchanged preferred ratio (collapsing the sidebar entirely below the
// breakpoint, same as before this feature); user resize -> change the
// preferred ratio, then recompute actual widths from it. A user's 45/55
// split survives a narrow-then-wide-again terminal resize because nothing
// about that sequence ever touched the stored ratio — the sidebar simply
// wasn't rendered while too narrow to show it.
const (
	// monitorDefaultSavedPacketsRatio is the sidebar's share of the
	// splittable width with no stored preference — chosen to match this
	// feature's predecessor (the automatic sidebar): at a representative
	// ~100-column terminal the old extra/3 heuristic gave the sidebar
	// roughly a third of the space left over both panes' floors, which
	// this ratio reproduces closely enough that upgrading users see no
	// visible jump in Monitor's default layout.
	monitorDefaultSavedPacketsRatio = 0.30

	// monitorSplitStep is how much one Left/Right keypress moves the
	// preferred ratio — a percentage of the splittable width rather than a
	// fixed column count, so the step still feels proportional at very
	// wide or very narrow terminals. At a typical ~100-column terminal
	// this works out to roughly 2-4 columns per press (task guidance) —
	// smooth under key-repeat, not a single-column crawl.
	monitorSplitStep = 0.03

	// monitorSplitSaveDebounce is how long a Left/Right/Reset keypress
	// waits with no further resize activity before the preference is
	// actually written to disk (see updateMonitorSaved / the
	// monitorSplitSaveMsg handling in Update()) — holding a resize key
	// under repeat updates the in-memory/rendered ratio on every keypress
	// but writes app.yaml at most once per settled adjustment, not once
	// per repeat event.
	monitorSplitSaveDebounce = 300 * time.Millisecond
)

// normalizedMonitorSplitRatio clamps/defaults a stored ratio to something
// safe to render with: a persisted 0 (never set, or an app.yaml predating
// this feature), a negative value, NaN, +Inf, or anything >= 1 all fall
// back to monitorDefaultSavedPacketsRatio rather than producing a broken or
// degenerate split — malformed config must never break Monitor's layout
// (task requirement). A small, self-contained numeric helper — not because
// this needs to be a generic pane-management primitive, just because the
// same normalization is needed both when rendering and when computing the
// next ratio a keypress produces.
func normalizedMonitorSplitRatio(r float64) float64 {
	if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 || r >= 1 {
		return monitorDefaultSavedPacketsRatio
	}
	return r
}

// effectiveMonitorSplitRatio is the ratio actually used to render — the
// stored preference, normalized/defaulted/clamped safe.
func (m *model) effectiveMonitorSplitRatio() float64 {
	return normalizedMonitorSplitRatio(m.app.UI.MonitorSavedPacketsRatio)
}

// monitorSplitAvailable is the total column budget the two panes' *content*
// widths (boxStyle.Width() arguments) divide between them at the current
// terminal width — total width minus both panes' border overhead and the
// gap between them. This is what the preferred ratio is a fraction OF.
func (m *model) monitorSplitAvailable() int {
	return m.width - monitorBoxOverhead*2 - monitorPaneGap
}

// monitorSidebarVisible reports whether the terminal is wide enough to show
// the sidebar alongside a still-useful traffic pane — both panes' own
// rendered (border+padding-inclusive) width, plus the gap between them,
// must fit. Below this, Monitor falls back to the existing full-width
// traffic view — the dedicated Packets → Saved screen and packet hotkeys
// remain fully available either way (see ARCHITECTURE.md "Hotkey policy").
// This check is independent of the preferred split ratio on purpose: the
// breakpoint is about whether both *minimums* fit at all, not about
// whatever share the user last asked for.
func (m *model) monitorSidebarVisible() bool {
	need := monitorTrafficMinWidth + monitorBoxOverhead +
		monitorPaneGap +
		monitorSidebarMinWidth + monitorBoxOverhead
	return m.width >= need
}

// monitorSidebarWidth is the sidebar's actual rendered *content* width
// (before boxStyle's own border/padding) once it's visible: the preferred
// ratio applied to the current terminal's splittable budget
// (monitorSplitAvailable), then clamped so neither pane can ever drop below
// its minimum — floored at monitorSidebarMinWidth, and capped so the
// traffic pane always keeps at least monitorTrafficMinWidth. This is a pure
// function of the *current* m.width and the *stored* ratio; it never
// mutates the stored preference (see the "adjustable split" doc comment
// above for why that distinction matters).
func (m *model) monitorSidebarWidth() int {
	available := m.monitorSplitAvailable()
	w := int(math.Round(float64(available) * m.effectiveMonitorSplitRatio()))
	if w < monitorSidebarMinWidth {
		w = monitorSidebarMinWidth
	}
	if max := available - monitorTrafficMinWidth; w > max {
		w = max
	}
	return w
}

// monitorSidebarState is the sidebar's own UI state — a selection cursor
// and scroll offset into whatever filteredSavedPackets currently returns,
// plus a save-debounce generation counter for the adjustable split (see
// updateMonitorSaved/monitorSplitSaveMsg). Deliberately not a copy of any
// Saved Packet data, and deliberately not where the split ratio itself
// lives — that's a persisted UI preference (config.App.UI, see
// "adjustable split" above), not ephemeral sidebar state.
type monitorSidebarState struct {
	cursor int
	scroll int

	// saveGen is bumped on every ratio-changing keypress. A scheduled
	// monitorSplitSaveMsg carries the generation it was scheduled for;
	// Update() only actually writes to disk if saveGen still matches when
	// the message arrives, so a burst of keypresses under repeat collapses
	// into a single write after things settle, not one write per repeat
	// event.
	saveGen int
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

// updateMonitorSaved handles keys while the sidebar has focus: select,
// send, and — new in this feature — resize/reset the split. Enter goes
// straight through sendSavedPacket — the exact function the dedicated
// Saved Packets screen's direct-send and the global hotkey dispatch both
// call — so validation, AUTO CRC recalculation, the not-connected/
// incompatible status wording, and the actual bytes put on the wire are
// all identical to every other way of sending a Saved Packet in the app.
// No second build/send path.
//
// Left/Right and "r" (resize/reset) were chosen only after checking
// keybindings.go's centralized policy: "left"/"right" are already globally
// reserved as generic "navigate" (excluded from hotkeyPalette, so a Saved
// Packet can never be assigned either as a hotkey) and — critically — were
// not previously bound to anything at all in Monitor, in either pane's
// dispatch; "r" is likewise already outside hotkeyPalette (reserved,
// labeled "rescan / refresh / rename" for other screens) and, again, is
// not bound to anything by Monitor itself. None of the three collide with
// a user-assignable Saved Packet hotkey, and resize/reset only fire while
// the sidebar has focus, so they never touch the traffic pane's own p/c/m.
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
			return m, m.sendSavedPacket(sp, "")
		}
	case "left":
		// Divider moves left -> Saved Packets gets wider.
		if m.resizeMonitorSplit(monitorSplitStep) {
			return m, m.scheduleMonitorSplitSave()
		}
	case "right":
		// Divider moves right -> Traffic gets wider.
		if m.resizeMonitorSplit(-monitorSplitStep) {
			return m, m.scheduleMonitorSplitSave()
		}
	case "r":
		if m.app.UI.MonitorSavedPacketsRatio != monitorDefaultSavedPacketsRatio {
			m.app.UI.MonitorSavedPacketsRatio = monitorDefaultSavedPacketsRatio
			m.monitorSaved.saveGen++
			m.status = "Monitor split reset to default"
			return m, m.scheduleMonitorSplitSave()
		}
	}
	return m, nil
}

// resizeMonitorSplit adjusts the preferred split ratio by delta (positive
// widens the sidebar, negative widens the traffic pane). The adjustment is
// clamped in *column* space against the current terminal width — converted
// to a ratio only afterward — so a resize that would push either pane past
// its minimum clamps exactly to that boundary instead of silently doing
// nothing (task requirement) or producing a ratio that briefly overshoots
// before the next render re-clamps it. Reports whether the stored
// preference actually changed, so a keypress at an already-reached boundary
// doesn't bump the save generation (and schedule a write) for nothing.
func (m *model) resizeMonitorSplit(delta float64) bool {
	available := m.monitorSplitAvailable()
	if available <= 0 {
		return false
	}
	current := m.effectiveMonitorSplitRatio()
	next := current + delta
	minRatio := float64(monitorSidebarMinWidth) / float64(available)
	maxRatio := float64(available-monitorTrafficMinWidth) / float64(available)
	if next < minRatio {
		next = minRatio
	}
	if next > maxRatio {
		next = maxRatio
	}
	if next == current {
		return false
	}
	m.app.UI.MonitorSavedPacketsRatio = next
	m.monitorSaved.saveGen++
	return true
}

// scheduleMonitorSplitSave returns the tea.Cmd that debounces persisting
// the Monitor split ratio: monitorSplitSaveDebounce after this call, with
// no further ratio change in between, Update() writes m.app to app.yaml
// via config.SaveApp — the existing atomic-write path, no new persistence
// mechanism (task requirement: reuse the existing config infrastructure,
// no separate preference file). Every resize/reset keypress calls this and
// gets its own tea.Cmd, but each carries the generation counter current at
// the moment it was scheduled; Update() only actually saves if that
// generation is still current when the tick fires, so holding a resize key
// under terminal key-repeat collapses into a single write once the user
// settles, not one write per repeat event.
func (m *model) scheduleMonitorSplitSave() tea.Cmd {
	gen := m.monitorSaved.saveGen
	return tea.Tick(monitorSplitSaveDebounce, func(time.Time) tea.Msg {
		return monitorSplitSaveMsg{gen: gen}
	})
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
