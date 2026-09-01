package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
)

// DiagramOptions configures one render of the register-style packet
// diagram — the single reusable visualization the product spec insists on
// (§7: "This packet diagram should become a reusable component throughout
// the application"). Every screen that shows a packet's shape (the
// protocol designer, TX builder, RX inspector, packet inspector, batch's
// live packet view) calls RenderDiagram with the same Schema.Layout(); none
// of them hand-roll their own box art. There is exactly one implementation
// of this rendering on purpose — see the package doc comment.
type DiagramOptions struct {
	Width int // available terminal columns; <=0 falls back to 80

	// Selected, if >= 0, is the index into Schema.Fields to highlight (the
	// designer's edit cursor). -1 means no selection.
	Selected int

	// Values, if non-nil, are shown as a third line per cell (hex bytes,
	// truncated to fit) — the TX builder/RX inspector/packet inspector use
	// this; the designer (which has no packet instance yet) leaves it nil.
	Values packet.Values

	// CRCResult, if non-nil, replaces the CRC cell's value row with either a
	// PASS/FAIL comparison or the actual AUTO/OVERRIDE byte, per CRCDisplay
	// — RX inspection and TX preview both set this; the designer (no packet
	// instance yet) leaves it nil, and the cell falls back to showing
	// nothing in the value row.
	CRCResult *packet.CRCResult

	// CRCDisplay selects how CRCResult is rendered in the CRC cell's value
	// row and border color. Ignored when CRCResult is nil. Zero value
	// (CRCDisplayCompare) is the RX/decode reading: "does the received
	// byte match a recalculation" — PASS/FAIL, green/red. CRCDisplayAuto is
	// the TX/build reading: "what byte will actually go out, and was it
	// AUTO or a manual OVERRIDE" — never PASS/FAIL, since a TX packet's CRC
	// agreeing with its own arithmetic is not the same claim as a device
	// having confirmed it (see packet.CRCResult's doc comment).
	CRCDisplay CRCDisplayMode
}

// CRCDisplayMode selects how a CRC cell with a non-nil CRCResult renders
// its value row — see DiagramOptions.CRCDisplay.
type CRCDisplayMode int

const (
	CRCDisplayCompare CRCDisplayMode = iota // RX: PASS/FAIL from Received vs. Calculated
	CRCDisplayAuto                          // TX: the actual byte value, tagged AUTO or OVERRIDE
)

// RenderDiagram draws schema's byte layout as one or more rows of merged
// box-drawing cells, proportioned to each field's byte size and packed
// into DiagramOptions.Width — wrapping into additional rows (each its own
// self-contained diagram with a "bytes N–M" caption) rather than shrinking
// cells into illegibility when the packet doesn't fit in one row (§7's
// "horizontal scrolling / multiple rows / compact representations"
// requirement; this implementation picks multiple rows).
func RenderDiagram(schema packet.Schema, opts DiagramOptions) string {
	width := opts.Width
	if width <= 0 {
		width = 80
	}
	spans := schema.Layout()
	if len(spans) == 0 {
		return unallocStyle.Render(fmt.Sprintf("EMPTY PACKET — set a total size to begin (0 / %d B)", schema.TotalSize))
	}

	rows := packRows(spans, width)
	var out []string
	for _, row := range rows {
		if len(rows) > 1 {
			out = append(out, dimStyle.Render(fmt.Sprintf("bytes %d–%d", row[0].Offset, row[len(row)-1].End()-1)))
		}
		out = append(out, renderRow(schema.Checksum, row, width, opts))
	}
	out = append(out, summaryLine(schema, width))
	return strings.Join(out, "\n")
}

// cellMinWidth is the narrowest a span's cell can render its label without
// truncation ugliness — at least "NAME" plus one padding column each side,
// and never less than 6 so the size/value lines have somewhere to go.
//
// A CRC span's Name is the checksum's full algorithm name (e.g.
// "CRC-8/MAXIM-DOW"), which would otherwise force every CRC cell — even in
// a packed, many-field row — to reserve 18+ columns just for a label that
// renderRow abbreviates anyway (crcCellLabel picks a shorter catalogued
// alias, or ellipsis-truncates, to fit whatever width it's actually given).
// So CRC spans use the same small floor as any other cell instead of their
// full name's length, keeping the "responsive abbreviation, not a wrecked
// layout" behavior this floor exists to protect.
func cellMinWidth(sp packet.Span) int {
	if sp.Kind == packet.SpanCRC {
		return 6
	}
	w := len(sp.Name) + 2
	if w < 6 {
		w = 6
	}
	return w
}

// packRows greedily packs spans into rows so each row's minimum width fits
// within width, always placing at least one span per row (a single span
// wider than the terminal still gets its own row rather than being
// dropped).
func packRows(spans []packet.Span, width int) [][]packet.Span {
	var rows [][]packet.Span
	var cur []packet.Span
	curWidth := 1 // the row's single leading border column
	for _, sp := range spans {
		mw := cellMinWidth(sp) + 1 // + this cell's own trailing border column
		if len(cur) > 0 && curWidth+mw > width {
			rows = append(rows, cur)
			cur = nil
			curWidth = 1
		}
		cur = append(cur, sp)
		curWidth += mw
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	return rows
}

// distributeWidths assigns each span in row a character width proportional
// to its byte size, respecting each cell's minimum and using the largest-
// remainder method so the widths sum to exactly totalWidth (when possible).
func distributeWidths(row []packet.Span, totalWidth int) []int {
	mins := make([]int, len(row))
	sumMin := 0
	sumSize := 0
	for i, sp := range row {
		mins[i] = cellMinWidth(sp)
		sumMin += mins[i]
		sumSize += sp.Size
	}
	widths := append([]int(nil), mins...)
	extra := totalWidth - sumMin
	if extra <= 0 || sumSize == 0 {
		return widths // row doesn't fit even at minimums (very narrow terminal) — degrade to minimums
	}
	type frac struct {
		i   int
		rem float64
	}
	fracs := make([]frac, len(row))
	assigned := 0
	for i, sp := range row {
		share := float64(extra) * float64(sp.Size) / float64(sumSize)
		whole := int(share)
		widths[i] += whole
		assigned += whole
		fracs[i] = frac{i, share - float64(whole)}
	}
	remainder := extra - assigned
	// Largest fractional remainder gets the leftover columns, one each.
	for remainder > 0 {
		best := 0
		for i := 1; i < len(fracs); i++ {
			if fracs[i].rem > fracs[best].rem {
				best = i
			}
		}
		widths[fracs[best].i]++
		fracs[best].rem = -1 // consumed
		remainder--
	}
	return widths
}

// renderRow draws one row of adjacent cells sharing merged borders: each
// cell contributes its own right-hand wall (┬/│/┴ or ┐/│/┘ for the last
// cell), and only the first cell contributes the row's opening wall — that
// way two adjacent cells never draw two joint characters on top of each
// other, and each wall segment is colored by the cell that owns it.
func renderRow(crcDef checksum.Definition, row []packet.Span, totalWidth int, opts DiagramOptions) string {
	// Border columns: one leading wall for the row plus one trailing wall
	// per cell — distributeWidths only needs to fill the content columns.
	contentWidth := totalWidth - (len(row) + 1)
	if contentWidth < 0 {
		contentWidth = 0
	}
	widths := distributeWidths(row, contentWidth)
	haveValues := opts.Values != nil

	var top, name, meta, value, bottom strings.Builder
	for i, sp := range row {
		w := widths[i]
		border, text := cellStyle(sp, opts)
		selected := opts.Selected >= 0 && sp.FieldIndex == opts.Selected
		first := i == 0
		last := i == len(row)-1

		if first {
			top.WriteString(border.Render("┌"))
			name.WriteString(border.Render("│"))
			meta.WriteString(border.Render("│"))
			value.WriteString(border.Render("│"))
			bottom.WriteString(border.Render("└"))
		}

		topJoint, botJoint := "┬", "┴"
		if last {
			topJoint, botJoint = "┐", "┘"
		}
		top.WriteString(border.Render(strings.Repeat("─", w) + topJoint))
		bottom.WriteString(border.Render(strings.Repeat("─", w) + botJoint))

		label := sp.Name
		if sp.Kind == packet.SpanCRC {
			label = crcCellLabel(crcDef, w)
		}
		if selected {
			label = "▸" + label
		}
		name.WriteString(text.Render(centerText(label, w)) + border.Render("│"))
		meta.WriteString(dimStyle.Render(centerText(cellMeta(sp), w)) + border.Render("│"))
		if haveValues {
			value.WriteString(text.Render(centerText(cellValue(sp, opts, w), w)) + border.Render("│"))
		}
	}

	lines := []string{top.String(), name.String(), meta.String()}
	if haveValues {
		lines = append(lines, value.String())
	}
	lines = append(lines, bottom.String())
	return strings.Join(lines, "\n")
}

func cellStyle(sp packet.Span, opts DiagramOptions) (border, text lipgloss.Style) {
	selected := opts.Selected >= 0 && sp.FieldIndex == opts.Selected
	switch {
	case selected:
		return lipgloss.NewStyle().Foreground(selectedBorder), lipgloss.NewStyle().Bold(true).Foreground(selectedBorder)
	case sp.Kind == packet.SpanCRC:
		// PASS/FAIL green-red only applies to CRCDisplayCompare (RX: a
		// received byte compared against a recalculation). CRCDisplayAuto
		// (TX) never colors by Valid — a freshly built packet is Valid by
		// construction whenever it isn't manually overridden, and coloring
		// that green would read as a confirmation nothing has actually
		// confirmed; see packet.CRCResult's doc comment.
		if opts.CRCResult != nil && opts.CRCDisplay == CRCDisplayCompare {
			if opts.CRCResult.Valid {
				return lipgloss.NewStyle().Foreground(lipgloss.Color("42")), lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
			}
			return lipgloss.NewStyle().Foreground(lipgloss.Color("203")), lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
		}
		return lipgloss.NewStyle().Foreground(crcBorder), crcTextStyle
	case sp.Kind == packet.SpanUnallocated:
		return lipgloss.NewStyle().Foreground(unallocBorder), unallocStyle
	default:
		return lipgloss.NewStyle().Foreground(normalBorder), fieldTextStyle
	}
}

// crcCellLabel picks the longest checksum.Definition.AlgorithmLabels
// candidate that fits within w display columns — e.g. the full
// "CRC-8/MAXIM-DOW" at a generous width, its catalogued "CRC-8/MAXIM" alias
// once the cell narrows, all the way down to whatever centerText's own
// ellipsis truncation makes of the shortest candidate. Algorithm naming
// itself is entirely checksum.Definition's job; this only ever chooses
// which already-named candidate fits, never invents an abbreviation.
func crcCellLabel(def checksum.Definition, w int) string {
	labels := def.AlgorithmLabels()
	if len(labels) == 0 {
		return "CRC"
	}
	best, bestLen := labels[0], -1
	shortest := labels[0]
	for _, l := range labels {
		n := len([]rune(l))
		if n <= w && n > bestLen {
			best, bestLen = l, n
		}
		if n < len([]rune(shortest)) {
			shortest = l
		}
	}
	if bestLen >= 0 {
		return best
	}
	return shortest // still too long for w; centerText truncates it with "…"
}

// crcHexValue formats a CRC's numeric value as uppercase hex, zero-padded
// to the algorithm's actual byte width (so an 8-bit CRC of 0x05 reads "05",
// not "5") — the same convention hexBytes uses for field values.
func crcHexValue(widthBits int, v uint64) string {
	byteWidth := (widthBits + 7) / 8
	if byteWidth < 1 {
		byteWidth = 1
	}
	return fmt.Sprintf("%0*X", byteWidth*2, v)
}

func cellMeta(sp packet.Span) string {
	if sp.Size == 1 {
		return fmt.Sprintf("%d B · @%d", sp.Size, sp.Offset)
	}
	return fmt.Sprintf("%d B · %d–%d", sp.Size, sp.Offset, sp.End()-1)
}

func cellValue(sp packet.Span, opts DiagramOptions, w int) string {
	if sp.Kind == packet.SpanUnallocated {
		return ""
	}
	if sp.Kind == packet.SpanCRC {
		if opts.CRCResult == nil {
			// e.g. the designer, which has no packet instance yet — Values
			// never carries the CRC by field name since it isn't a Field,
			// so there's nothing to show.
			return ""
		}
		if opts.CRCDisplay == CRCDisplayAuto {
			return crcAutoValueCell(*opts.CRCResult, w)
		}
		if opts.CRCResult.Valid {
			return "PASS"
		}
		return "FAIL"
	}
	raw, ok := opts.Values[sp.Name]
	if !ok {
		return "…"
	}
	return hexBytes(raw)
}

// crcAutoValueCell renders CRCDisplayAuto's value-row text in tiers that
// shrink to fit w, the same "degrade rather than mid-word-truncate"
// approach summaryLine uses: "09 · AUTO" at full width, "09 OVR" once
// "OVERRIDE" no longer fits, down to the bare value if even that doesn't —
// so a cramped cell never renders an ellipsis-mangled "OVER…".
func crcAutoValueCell(r packet.CRCResult, w int) string {
	val := crcHexValue(r.Width, r.Received)
	modeFull, modeShort := "AUTO", "AUTO"
	if r.Manual {
		modeFull, modeShort = "OVERRIDE", "OVR"
	}
	if full := val + " · " + modeFull; len([]rune(full)) <= w {
		return full
	}
	if compact := val + " " + modeShort; len([]rune(compact)) <= w {
		return compact
	}
	return val
}

func hexBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	return strings.Join(parts, " ")
}

// centerText pads/truncates s to exactly w display columns. It measures
// and slices by rune, not byte, since cell text routinely contains
// multi-byte runes ("·", "–", "▸") that a byte-length/byte-slice would
// miscount or cut mid-character.
func centerText(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		if w <= 1 {
			if w == 1 {
				return string(r[:1])
			}
			return ""
		}
		return string(r[:w-1]) + "…"
	}
	total := w - len(r)
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}

// summaryLine reports name/total/allocated/remaining/validity below the
// diagram, shrinking to fit narrow widths in tiers (full → compact →
// minimal) rather than either overflowing width or silently dropping
// information a wide terminal would show.
func summaryLine(schema packet.Schema, width int) string {
	allocated := schema.Allocated()
	remaining := schema.Remaining()
	valid := schema.Validate() == nil
	statusWord := "valid"
	statusStyle := okStyle
	if !valid {
		statusWord = "invalid"
		statusStyle = badStyle
	}

	full := fmt.Sprintf("%s   %s %d B   %s %d B   %s %d B   %s",
		sectionStyle.Render(schema.Name),
		dimStyle.Render("total"), schema.TotalSize,
		dimStyle.Render("allocated"), allocated,
		dimStyle.Render("remaining"), remaining,
		statusStyle.Render(statusWord),
	)
	if lipgloss.Width(full) <= width {
		return full
	}

	compact := fmt.Sprintf("%s %s %d/%d B %s",
		sectionStyle.Render(schema.Name),
		dimStyle.Render("·"), allocated, schema.TotalSize,
		statusStyle.Render(statusWord),
	)
	if lipgloss.Width(compact) <= width {
		return compact
	}

	return fmt.Sprintf("%d/%d B %s", allocated, schema.TotalSize, statusStyle.Render(statusWord))
}
