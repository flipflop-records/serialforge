package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
)

// The protocol designer (product spec §6/§8/§9/§10 — the milestone the
// spec explicitly says not to postpone). It edits one draft packet.Schema
// in place and re-renders RenderDiagram after every change, so the diagram
// is always a live view of exactly what Serialize/Decode would do with the
// current draft — never a second, hand-maintained picture of the packet.
type designerMode int

const (
	dmBrowse designerMode = iota
	dmField               // add/edit one field: name, size, format
	dmTotalSize
	dmSaveName
	dmLoadPicker
	dmCRCPreset
	dmCRCCustom
)

var fieldFormatOptions = []packet.Format{packet.FormatHex, packet.FormatUint, packet.FormatInt, packet.FormatASCII, packet.FormatRaw}

type designerState struct {
	schema packet.Schema
	// cursor indexes the designer's row list: 0 = total size, 1..len(Fields)
	// = schema.Fields[cursor-1] in packet order, and the last row
	// (rowCount()-1) is always the checksum row — see the tail-checksum
	// invariant on rowCount/activateRow below. A tail CRC is never a
	// schema.Fields entry (see packet.Schema.CRCOffset), so this ordering
	// falls out of the schema itself rather than being a TUI-only choice.
	cursor     int
	mode       designerMode
	loadedName string
	message    string

	fieldEditIndex int // -1 while adding a new field
	fieldName      string
	fieldSize      string
	fieldFormatIdx int
	fieldFocus     int // 0=name, 1=size, 2=format

	totalSizeBuf string
	saveNameBuf  string
	loadCursor   int
	presetCursor int

	customWidth  string
	customPoly   string
	customInit   string
	customRefIn  bool
	customRefOut bool
	customXorOut string
	customCursor int
}

func newDesignerState() designerState {
	return designerState{schema: packet.Schema{Name: "untitled", TotalSize: 0}}
}

// rowCount is 1 (packet size) + one row per user field + 1 (the checksum
// row, always present — even a disabled checksum gets a row so it can be
// enabled — and always last, per the tail-checksum invariant).
func (d *designerState) rowCount() int { return 2 + len(d.schema.Fields) }

// checksumRow is the row index of the tail checksum — always the last row.
// Every place that needs to know "is the cursor on the checksum row" or
// "where does the checksum row render" goes through this one definition so
// the invariant can't drift out of sync between navigation and rendering.
func (d *designerState) checksumRow() int { return d.rowCount() - 1 }

// cursorField reports the schema.Fields index the cursor currently sits on,
// and false when the cursor is on the packet-size row or the tail checksum
// row — the single mapping every field-mutating action (delete/duplicate/
// reorder) uses, so none of them can ever mistake the checksum row for a
// normal field.
func (d *designerState) cursorField() (int, bool) {
	idx := d.cursor - 1
	if idx < 0 || idx >= len(d.schema.Fields) {
		return 0, false
	}
	return idx, true
}

// handleKeyIfEditing intercepts keys for every designer sub-form. It only
// activates while the Packets/Designer subview is actually on screen, so
// leaving a form open and switching tabs can't hijack another screen's
// keys.
func (d *designerState) handleKeyIfEditing(m *model, msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.tab != tabPackets || m.packetsView != packetsDesigner || d.mode == dmBrowse {
		return nil, false
	}
	switch d.mode {
	case dmField:
		return nil, d.handleFieldForm(msg)
	case dmTotalSize:
		return nil, d.handleTotalSizeForm(msg)
	case dmSaveName:
		return nil, d.handleSaveNameForm(m, msg)
	case dmLoadPicker:
		return nil, d.handleLoadPicker(m, msg)
	case dmCRCPreset:
		return nil, d.handleCRCPreset(msg)
	case dmCRCCustom:
		return nil, d.handleCRCCustom(msg)
	}
	return nil, false
}

// --- field add/edit form -----------------------------------------------------

func (d *designerState) openFieldForm(editIndex int) {
	d.mode = dmField
	d.fieldEditIndex = editIndex
	d.fieldFocus = 0
	if editIndex >= 0 {
		f := d.schema.Fields[editIndex]
		d.fieldName = f.Name
		d.fieldSize = strconv.Itoa(f.Size)
		d.fieldFormatIdx = formatIndex(f.EffectiveFormat())
	} else {
		d.fieldName = ""
		d.fieldSize = ""
		d.fieldFormatIdx = 0
	}
}

func formatIndex(f packet.Format) int {
	for i, opt := range fieldFormatOptions {
		if opt == f {
			return i
		}
	}
	return 0
}

// the field form has two text sub-fields (name, size) and one cycling
// sub-field (format), moved between explicitly via Tab/Shift+Tab — the
// same focus idiom as the custom-CRC and add-device forms.
func (d *designerState) handleFieldForm(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEsc:
		d.mode = dmBrowse
		return true
	case tea.KeyEnter:
		return d.submitFieldForm()
	case tea.KeyTab, tea.KeyDown:
		d.fieldFocus = (d.fieldFocus + 1) % 3
		return true
	case tea.KeyShiftTab, tea.KeyUp:
		d.fieldFocus = (d.fieldFocus - 1 + 3) % 3
		return true
	}
	if d.fieldFocus == 2 {
		switch msg.Type {
		case tea.KeyLeft:
			d.fieldFormatIdx = (d.fieldFormatIdx - 1 + len(fieldFormatOptions)) % len(fieldFormatOptions)
		case tea.KeyRight:
			d.fieldFormatIdx = (d.fieldFormatIdx + 1) % len(fieldFormatOptions)
		}
		return true
	}
	buf := &d.fieldName
	if d.fieldFocus == 1 {
		buf = &d.fieldSize
	}
	switch msg.Type {
	case tea.KeyBackspace:
		if len(*buf) > 0 {
			*buf = (*buf)[:len(*buf)-1]
		}
	case tea.KeyRunes:
		*buf += string(msg.Runes)
	}
	return true
}

func (d *designerState) submitFieldForm() bool {
	name := strings.TrimSpace(d.fieldName)
	size, err := strconv.Atoi(strings.TrimSpace(d.fieldSize))
	if name == "" || err != nil || size < 1 {
		d.message = "field needs a name and a size >= 1"
		return true
	}
	f := packet.Field{Name: name, Size: size, Format: fieldFormatOptions[d.fieldFormatIdx]}
	if d.fieldEditIndex >= 0 {
		d.schema.Fields[d.fieldEditIndex] = f
	} else {
		d.schema.Fields = append(d.schema.Fields, f)
	}
	d.mode = dmBrowse
	d.message = ""
	return true
}

// --- total size form ---------------------------------------------------------

func (d *designerState) handleTotalSizeForm(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEsc:
		d.mode = dmBrowse
	case tea.KeyEnter:
		if n, err := strconv.Atoi(strings.TrimSpace(d.totalSizeBuf)); err == nil && n > 0 {
			d.schema.TotalSize = n
			d.mode = dmBrowse
		} else {
			d.message = "enter a whole number of bytes > 0"
		}
	case tea.KeyBackspace:
		if len(d.totalSizeBuf) > 0 {
			d.totalSizeBuf = d.totalSizeBuf[:len(d.totalSizeBuf)-1]
		}
	case tea.KeyRunes:
		d.totalSizeBuf += string(msg.Runes)
	}
	return true
}

// --- save-as-profile form -----------------------------------------------------

func (d *designerState) handleSaveNameForm(m *model, msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEsc:
		d.mode = dmBrowse
	case tea.KeyEnter:
		name := strings.TrimSpace(d.saveNameBuf)
		if name == "" {
			d.message = "enter a name"
			return true
		}
		d.schema.Name = name
		if err := m.cfg.Protocols.Put(d.schema); err != nil {
			d.message = err.Error()
			return true
		}
		if err := m.cfg.Protocols.Save(); err != nil {
			d.message = err.Error()
			return true
		}
		d.loadedName = name
		d.mode = dmBrowse
		m.status = "saved protocol " + name
	case tea.KeyBackspace:
		if len(d.saveNameBuf) > 0 {
			d.saveNameBuf = d.saveNameBuf[:len(d.saveNameBuf)-1]
		}
	case tea.KeyRunes:
		d.saveNameBuf += string(msg.Runes)
	}
	return true
}

// --- load picker ---------------------------------------------------------------

func (d *designerState) handleLoadPicker(m *model, msg tea.KeyMsg) bool {
	names := m.cfg.Protocols.Names()
	switch msg.String() {
	case "esc":
		d.mode = dmBrowse
	case "up", "k":
		if d.loadCursor > 0 {
			d.loadCursor--
		}
	case "down", "j":
		if d.loadCursor < len(names)-1 {
			d.loadCursor++
		}
	case "enter":
		if d.loadCursor < len(names) {
			sc, _ := m.cfg.Protocols.Get(names[d.loadCursor])
			d.schema = sc.Clone()
			d.loadedName = sc.Name
			d.cursor = 0
			d.mode = dmBrowse
		}
	}
	return true
}

// --- CRC preset picker ------------------------------------------------------

func (d *designerState) openCRCPicker() {
	d.mode = dmCRCPreset
	d.presetCursor = 0
	for i, name := range checksum.Names() {
		if name == d.schema.Checksum.Preset {
			d.presetCursor = i
			break
		}
	}
}

func (d *designerState) handleCRCPreset(msg tea.KeyMsg) bool {
	names := checksum.Names()
	switch msg.String() {
	case "esc":
		d.mode = dmBrowse
	case "up", "k":
		if d.presetCursor > 0 {
			d.presetCursor--
		}
	case "down", "j":
		if d.presetCursor < len(names)-1 {
			d.presetCursor++
		}
	case "n":
		d.schema.Checksum = checksum.Definition{Mode: checksum.ModeNone}
		d.mode = dmBrowse
	case "u":
		d.openCRCCustom()
	case "enter":
		if d.presetCursor < len(names) {
			d.schema.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: names[d.presetCursor]}
			d.mode = dmBrowse
		}
	}
	return true
}

// --- custom CRC form -----------------------------------------------------------

func (d *designerState) openCRCCustom() {
	d.mode = dmCRCCustom
	p := d.schema.Checksum.Custom
	if d.schema.Checksum.Mode != checksum.ModeCustom {
		p = checksum.Params{Width: 8}
	}
	d.customWidth = strconv.Itoa(p.Width)
	d.customPoly = fmt.Sprintf("%X", p.Poly)
	d.customInit = fmt.Sprintf("%X", p.Init)
	d.customXorOut = fmt.Sprintf("%X", p.XorOut)
	d.customRefIn = p.RefIn
	d.customRefOut = p.RefOut
	d.customCursor = 0
}

var customFieldLabels = []string{"Width (bits)", "Polynomial (hex)", "Init (hex)", "RefIn", "RefOut", "XOR Out (hex)"}

func (d *designerState) handleCRCCustom(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyEsc:
		d.mode = dmBrowse
		return true
	case tea.KeyTab, tea.KeyDown:
		d.customCursor = (d.customCursor + 1) % len(customFieldLabels)
		return true
	case tea.KeyShiftTab, tea.KeyUp:
		d.customCursor = (d.customCursor - 1 + len(customFieldLabels)) % len(customFieldLabels)
		return true
	case tea.KeyEnter:
		return d.submitCRCCustom()
	}
	switch d.customCursor {
	case 0:
		editDigits(&d.customWidth, msg)
	case 1:
		editHex(&d.customPoly, msg)
	case 2:
		editHex(&d.customInit, msg)
	case 3:
		if msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight || msg.Type == tea.KeySpace {
			d.customRefIn = !d.customRefIn
		}
	case 4:
		if msg.Type == tea.KeyLeft || msg.Type == tea.KeyRight || msg.Type == tea.KeySpace {
			d.customRefOut = !d.customRefOut
		}
	case 5:
		editHex(&d.customXorOut, msg)
	}
	return true
}

func editDigits(buf *string, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyBackspace:
		if len(*buf) > 0 {
			*buf = (*buf)[:len(*buf)-1]
		}
	case tea.KeyRunes:
		*buf += string(msg.Runes)
	}
}

func editHex(buf *string, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyBackspace:
		if len(*buf) > 0 {
			*buf = (*buf)[:len(*buf)-1]
		}
	case tea.KeyRunes:
		*buf += strings.ToUpper(string(msg.Runes))
	}
}

func (d *designerState) submitCRCCustom() bool {
	width, err1 := strconv.Atoi(strings.TrimSpace(d.customWidth))
	poly, err2 := strconv.ParseUint(strings.TrimSpace(d.customPoly), 16, 64)
	init, err3 := strconv.ParseUint(strings.TrimSpace(d.customInit), 16, 64)
	xorOut, err4 := strconv.ParseUint(strings.TrimSpace(d.customXorOut), 16, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		d.message = "width must be decimal; polynomial/init/xorout must be hex"
		return true
	}
	params := checksum.Params{Width: width, Poly: poly, Init: init, XorOut: xorOut, RefIn: d.customRefIn, RefOut: d.customRefOut}
	if err := params.Validate(); err != nil {
		d.message = err.Error()
		return true
	}
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModeCustom, Custom: params}
	d.mode = dmBrowse
	d.message = ""
	return true
}

// --- browse mode: navigation + row actions ----------------------------------

func (m *model) updateDesigner(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.designer
	switch msg.String() {
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < d.rowCount()-1 {
			d.cursor++
		}
	case "enter":
		d.activateRow()
	case "n":
		d.openFieldForm(-1)
	case "x", "delete":
		// cursorField already excludes the checksum row, so deleting can
		// only ever remove a normal field — the tail checksum (not a
		// schema.Fields entry at all) is untouched and stays last.
		if idx, ok := d.cursorField(); ok {
			d.schema.Fields = append(d.schema.Fields[:idx], d.schema.Fields[idx+1:]...)
			if d.cursor >= d.rowCount() {
				d.cursor = d.rowCount() - 1
			}
		}
	case "d":
		// Insert right after the source field — still strictly among
		// schema.Fields, so the duplicate lands before the checksum row
		// the same way every other field does.
		if idx, ok := d.cursorField(); ok {
			dup := d.schema.Fields[idx]
			dup.Name += "_copy"
			d.schema.Fields = append(d.schema.Fields[:idx+1], append([]packet.Field{dup}, d.schema.Fields[idx+1:]...)...)
		}
	case "<", "H":
		// idx > 0 (not just idx-1 >= 0) keeps this a swap between two
		// normal fields; the checksum row isn't reachable via cursorField
		// so it can never take part in a reorder swap.
		if idx, ok := d.cursorField(); ok && idx > 0 {
			d.schema.Fields[idx-1], d.schema.Fields[idx] = d.schema.Fields[idx], d.schema.Fields[idx-1]
			d.cursor--
		}
	case ">", "L":
		// idx < len(Fields)-1 keeps the swap within schema.Fields — a
		// field can never be pushed past the last field into the tail
		// checksum's position, which is the reordering half of the
		// tail-checksum invariant.
		if idx, ok := d.cursorField(); ok && idx < len(d.schema.Fields)-1 {
			d.schema.Fields[idx+1], d.schema.Fields[idx] = d.schema.Fields[idx], d.schema.Fields[idx+1]
			d.cursor++
		}
	case "s":
		d.mode = dmSaveName
		d.saveNameBuf = d.schema.Name
	case "o":
		d.mode = dmLoadPicker
		d.loadCursor = 0
	case "N":
		d.schema = newDesignerState().schema
		d.loadedName = ""
		d.cursor = 0
	}
	return m, nil
}

func (d *designerState) activateRow() {
	switch {
	case d.cursor == 0:
		d.mode = dmTotalSize
		d.totalSizeBuf = ""
		if d.schema.TotalSize > 0 {
			d.totalSizeBuf = strconv.Itoa(d.schema.TotalSize)
		}
	case d.cursor == d.checksumRow():
		d.openCRCPicker()
	default:
		if idx, ok := d.cursorField(); ok {
			d.openFieldForm(idx)
		}
	}
}

// --- rendering -----------------------------------------------------------------

func (m *model) viewDesigner() string {
	d := &m.designer
	switch d.mode {
	case dmTotalSize:
		return accentBox.Render(sectionStyle.Render("Total packet size") + "\n\n  " +
			keyStyle.Render(d.totalSizeBuf) + "█ bytes\n\n" + renderHints(hint("enter", "confirm"), hint("esc", "cancel")))
	case dmField:
		return m.viewFieldForm()
	case dmSaveName:
		return accentBox.Render(sectionStyle.Render("Save protocol as") + "\n\n  " +
			keyStyle.Render(d.saveNameBuf) + "█\n\n" + renderHints(hint("enter", "confirm"), hint("esc", "cancel")))
	case dmLoadPicker:
		return m.viewLoadPicker()
	case dmCRCPreset:
		return m.viewCRCPresetPicker()
	case dmCRCCustom:
		return m.viewCRCCustomForm()
	}

	var b strings.Builder
	title := d.schema.Name
	if d.loadedName != "" {
		title = d.loadedName
	}
	b.WriteString(sectionStyle.Render(title))
	if d.schema.Validate() != nil {
		b.WriteString("  " + warnStyle.Render("draft"))
	}
	b.WriteString("\n\n")

	row := func(i int, label, value string) {
		marker := "  "
		if i == d.cursor {
			marker = keyStyle.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%-14s %s\n", marker, label, value))
	}
	sizeLabel := "(unset)"
	if d.schema.TotalSize > 0 {
		sizeLabel = fmt.Sprintf("%d bytes", d.schema.TotalSize)
	}
	// Packet size is schema metadata and stays above the fields. Below it,
	// user fields render in packet order and the checksum row always comes
	// last — matching, byte for byte, the ordering Schema.Layout() hands
	// the register diagram just below, so the two can never disagree (see
	// checksumRow's doc comment).
	row(0, "Packet size", sizeLabel)
	for i, f := range d.schema.Fields {
		row(1+i, f.Name, fmt.Sprintf("%d B · %s", f.Size, f.EffectiveFormat()))
	}
	row(d.checksumRow(), "CRC", crcRowValue(d.schema))

	b.WriteString("\n")
	if d.schema.TotalSize > 0 {
		selected := -1
		if idx, ok := d.cursorField(); ok {
			selected = idx
		}
		b.WriteString(RenderDiagram(d.schema, DiagramOptions{Width: m.diagramWidth(), Selected: selected}))
	} else {
		b.WriteString(dimStyle.Render("Set a packet size to begin."))
	}
	if d.message != "" {
		b.WriteString("\n" + badStyle.Render(d.message))
	}
	b.WriteString("\n\n" + renderHints(
		hint("enter", "edit row"), hint("n", "new field"), hint("x", "delete"), hint("d", "duplicate"),
		hint("</>", "reorder"), hint("s", "save"), hint("o", "open"), hint("N", "new protocol")))
	return b.String()
}

// crcRowValue is the designer field-list's checksum row text: the reserved
// tail size plus the configured algorithm, e.g. "1 B · CRC-8/MAXIM-DOW" —
// so the row communicates the same reservation Schema.CRCOffset() (and
// therefore Layout()/the diagram/Serialize) actually makes, not just the
// algorithm's name. Routed through checksum.Definition.AlgorithmName so
// this, the TX Builder's CRC line, and the diagram's CRC cell all agree on
// one naming source instead of each inventing its own. "none" when the
// schema has no checksum configured.
func crcRowValue(schema packet.Schema) string {
	_, size, ok := schema.CRCOffset()
	if !ok {
		return dimStyle.Render("none")
	}
	return fmt.Sprintf("%d B · %s", size, schema.Checksum.AlgorithmName())
}

func (m *model) viewFieldForm() string {
	d := &m.designer
	title := "New field"
	if d.fieldEditIndex >= 0 {
		title = "Edit field"
	}
	row := func(i int, label, value string) string {
		marker := "  "
		if i == d.fieldFocus {
			marker = keyStyle.Render("▸ ")
			value += "█"
		}
		return fmt.Sprintf("%s%-8s %s", marker, label, value)
	}
	body := fmt.Sprintf("%s\n\n%s\n%s\n%s\n\n%s",
		sectionStyle.Render(title),
		row(0, "Name", d.fieldName),
		row(1, "Size", d.fieldSize),
		row(2, "Format", formatPicker(d.fieldFormatIdx)),
		renderHints(hint("tab/↓", "next field"), hint("←/→", "cycle format"), hint("enter", "confirm"), hint("esc", "cancel")))
	if d.message != "" {
		body += "\n" + badStyle.Render(d.message)
	}
	return accentBox.Render(body)
}

func formatPicker(idx int) string {
	parts := make([]string, len(fieldFormatOptions))
	for i, f := range fieldFormatOptions {
		if i == idx {
			parts[i] = keyStyle.Render("[" + string(f) + "]")
		} else {
			parts[i] = dimStyle.Render(string(f))
		}
	}
	return strings.Join(parts, "  ")
}

func (m *model) viewLoadPicker() string {
	d := &m.designer
	names := m.cfg.Protocols.Names()
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Open protocol") + "\n\n")
	if len(names) == 0 {
		b.WriteString(dimStyle.Render("  (no saved protocols yet)\n"))
	}
	for i, n := range names {
		marker := "  "
		if i == d.loadCursor {
			marker = keyStyle.Render("▸ ")
		}
		b.WriteString(marker + n + "\n")
	}
	b.WriteString("\n" + renderHints(hint("enter", "open"), hint("esc", "cancel")))
	return accentBox.Render(b.String())
}

func (m *model) viewCRCPresetPicker() string {
	d := &m.designer
	names := checksum.Names()
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Checksum") + "\n\n")
	for i, n := range names {
		marker := "  "
		if i == d.presetCursor {
			marker = keyStyle.Render("▸ ")
		}
		preset, _ := checksum.Lookup(n)
		b.WriteString(fmt.Sprintf("%s%-20s %s\n", marker, n, dimStyle.Render(fmt.Sprintf("%d-bit", preset.Params.Width))))
	}
	b.WriteString("\n" + renderHints(hint("enter", "select"), hint("n", "disable checksum"), hint("u", "custom CRC"), hint("esc", "cancel")))
	return accentBox.Render(b.String())
}

func (m *model) viewCRCCustomForm() string {
	d := &m.designer
	var b strings.Builder
	b.WriteString(sectionStyle.Render("Custom CRC") + "\n\n")
	values := []string{d.customWidth, d.customPoly, d.customInit, yesNo(d.customRefIn), yesNo(d.customRefOut), d.customXorOut}
	for i, label := range customFieldLabels {
		marker := "  "
		style := fieldTextStyle
		if i == d.customCursor {
			marker = keyStyle.Render("▸ ")
			style = keyStyle
		}
		b.WriteString(fmt.Sprintf("%s%-18s %s\n", marker, label, style.Render(values[i])))
	}
	if d.message != "" {
		b.WriteString("\n" + badStyle.Render(d.message))
	}
	b.WriteString("\n" + renderHints(
		hint("tab/↓", "next field"), hint("←/→/space", "toggle RefIn/RefOut"), hint("enter", "confirm"), hint("esc", "cancel")))
	return accentBox.Render(b.String())
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
