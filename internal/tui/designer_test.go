package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
)

// These tests pin down the Packet Designer's tail-checksum invariant: a
// checksum row is never a normal, freely reorderable schema.Fields entry —
// it always renders (and stays) after every user-defined field, exactly
// matching the ordering Schema.Layout()/CRCOffset() already enforce for
// serialization and for the register diagram. See checksumRow/cursorField
// in designer.go for where the invariant actually lives.

// baseDesignerSchema is a small 3-field starting point (no checksum yet)
// shared by the tests below.
func baseDesignerSchema() packet.Schema {
	return packet.Schema{
		Name:      "test",
		TotalSize: 6,
		Fields: []packet.Field{
			{Name: "Command", Size: 1, Format: packet.FormatHex},
			{Name: "Addr", Size: 4, Format: packet.FormatHex},
			{Name: "Data", Size: 1, Format: packet.FormatHex},
		},
	}
}

// crc8MaximDOW is the same preset newTestModel uses elsewhere: a
// deterministic 1-byte checksum, so tests can reason about exact reserved
// sizes without depending on checksum.Names()' ordering.
const crc8MaximDOW = "CRC-8/MAXIM-DOW"

func newDesignerTestModel(t *testing.T) *model {
	t.Helper()
	m := newTestModel(t)
	m.tab = tabPackets
	m.packetsView = packetsDesigner
	return m
}

func fieldsEqual(a, b []packet.Field) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Size != b[i].Size {
			return false
		}
	}
	return true
}

// 1. Enabling CRC places it after all fields.
func TestDesignerEnablingCRCPlacesItAfterAllFields(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.TotalSize = d.schema.FieldsSize() + 1 // room for a 1-byte CRC

	if d.schema.CRCSize() != 0 {
		t.Fatal("test setup: schema should start with no checksum")
	}
	d.cursor = d.checksumRow()
	d.activateRow() // opens the CRC preset picker, per the tail-only row
	if d.mode != dmCRCPreset {
		t.Fatalf("mode = %v, want dmCRCPreset", d.mode)
	}
	// Pick the deterministic preset explicitly rather than relying on
	// whatever checksum.Names() happens to list first.
	for i, name := range checksum.Names() {
		if name == crc8MaximDOW {
			d.presetCursor = i
			break
		}
	}
	pressKey(m, tea.KeyEnter)

	if d.mode != dmBrowse {
		t.Fatalf("mode = %v, want dmBrowse after picking a preset", d.mode)
	}
	if d.schema.CRCSize() != 1 {
		t.Fatalf("CRCSize = %d, want 1 after enabling %s", d.schema.CRCSize(), crc8MaximDOW)
	}
	off, size, ok := d.schema.CRCOffset()
	if !ok {
		t.Fatal("CRCOffset reports no checksum after enabling one")
	}
	if off != d.schema.FieldsSize() || size != 1 {
		t.Errorf("CRCOffset = (%d, %d), want (%d, 1) — CRC must sit right after all fields", off, size, d.schema.FieldsSize())
	}
	if d.checksumRow() != d.rowCount()-1 {
		t.Errorf("checksum row = %d, want the last row %d", d.checksumRow(), d.rowCount()-1)
	}
}

// 2. Adding a field while CRC is enabled keeps CRC last.
func TestDesignerAddingFieldWithCRCEnabledKeepsCRCLast(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: crc8MaximDOW}
	d.schema.TotalSize = d.schema.FieldsSize() + d.schema.CRCSize()

	rowBefore := d.checksumRow()

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if d.mode != dmField {
		t.Fatalf("mode = %v, want dmField after 'n'", d.mode)
	}
	typeString(m, "Extra")
	pressKey(m, tea.KeyTab)
	typeString(m, "1")
	pressKey(m, tea.KeyEnter)

	if got := d.schema.Fields[len(d.schema.Fields)-1].Name; got != "Extra" {
		t.Fatalf("new field not appended before the checksum: last field = %q", got)
	}
	if want := rowBefore + 1; d.checksumRow() != want {
		t.Errorf("checksum row = %d, want %d (pushed down by the new field)", d.checksumRow(), want)
	}
	off, _, ok := d.schema.CRCOffset()
	if !ok || off != d.schema.FieldsSize() {
		t.Errorf("CRC not at tail after adding a field: off=%d fieldsSize=%d ok=%v", off, d.schema.FieldsSize(), ok)
	}
}

// 3. Duplicating a field keeps CRC last.
func TestDesignerDuplicatingFieldKeepsCRCLast(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: crc8MaximDOW}
	d.schema.TotalSize = d.schema.FieldsSize() + d.schema.CRCSize()

	d.cursor = 1 // "Command", the first field row
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})

	if len(d.schema.Fields) != 4 {
		t.Fatalf("len(Fields) = %d, want 4 after duplicating", len(d.schema.Fields))
	}
	if d.schema.Fields[1].Name != "Command_copy" {
		t.Fatalf("duplicate not inserted right after its source: %+v", d.schema.Fields)
	}
	off, _, ok := d.schema.CRCOffset()
	if !ok || off != d.schema.FieldsSize() {
		t.Errorf("CRC no longer at tail after duplicating a field: off=%d fieldsSize=%d", off, d.schema.FieldsSize())
	}
	if d.checksumRow() != d.rowCount()-1 {
		t.Errorf("checksum row not last after duplicating: %d vs %d", d.checksumRow(), d.rowCount()-1)
	}
}

// 4. Reordering fields cannot move anything after tail CRC.
func TestDesignerReorderCannotMovePastTailCRC(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: crc8MaximDOW}
	d.schema.TotalSize = d.schema.FieldsSize() + d.schema.CRCSize()

	before := append([]packet.Field(nil), d.schema.Fields...)
	lastFieldRow := len(d.schema.Fields) // row 1 = Fields[0], so Fields[len-1] is row len(Fields)

	// Pushing the last field "right" must be a no-op: there is nothing
	// after it but the checksum, and a normal field can never cross that.
	d.cursor = lastFieldRow
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(">")})
	if !fieldsEqual(d.schema.Fields, before) {
		t.Errorf("last field moved past where the tail checksum must stay: %+v", d.schema.Fields)
	}
	if d.cursor != lastFieldRow {
		t.Errorf("cursor moved on a blocked reorder: got %d, want %d", d.cursor, lastFieldRow)
	}

	// Sitting on the checksum row itself, reordering keys must also be a
	// no-op — the checksum isn't a schema.Fields entry cursorField will
	// ever resolve to.
	d.cursor = d.checksumRow()
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<")})
	if !fieldsEqual(d.schema.Fields, before) {
		t.Errorf("fields changed when reordering from the checksum row: %+v", d.schema.Fields)
	}
	off, _, ok := d.schema.CRCOffset()
	if !ok || off != d.schema.FieldsSize() {
		t.Errorf("CRC not at tail after blocked reorders: off=%d fieldsSize=%d", off, d.schema.FieldsSize())
	}
}

// 5. CRC width change updates reserved tail size.
func TestDesignerCRCWidthChangeUpdatesReservedTailSize(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModeCustom, Custom: checksum.Params{Width: 8, Poly: 0x07}}
	d.schema.TotalSize = d.schema.FieldsSize() + d.schema.CRCSize()

	if got := d.schema.CRCSize(); got != 1 {
		t.Fatalf("test setup: CRCSize = %d, want 1", got)
	}
	beforeOff, _, _ := d.schema.CRCOffset()

	d.openCRCCustom()
	d.customWidth = "16"
	if !d.submitCRCCustom() {
		t.Fatal("submitCRCCustom returned false")
	}
	if d.mode != dmBrowse {
		t.Fatalf("mode = %v, want dmBrowse after a valid width change: %s", d.mode, d.message)
	}

	if got := d.schema.CRCSize(); got != 2 {
		t.Fatalf("CRCSize after widening to 16 bits = %d, want 2", got)
	}
	off, size, ok := d.schema.CRCOffset()
	if !ok {
		t.Fatal("CRCOffset not ok after width change")
	}
	if off != beforeOff {
		t.Errorf("CRC offset moved from %d to %d; only its width changed, its offset (end of fields) should not", beforeOff, off)
	}
	if size != 2 {
		t.Errorf("reserved CRC size = %d, want 2", size)
	}

	// The diagram-facing Layout() must reflect the new size immediately.
	spans := d.schema.Layout()
	if len(spans) == 0 || spans[len(spans)-1].Kind != packet.SpanCRC {
		// TotalSize is now under-allocated (8 needed, 7 configured), so
		// there is no trailing unallocated span — CRC is still the last span.
		t.Fatalf("Layout()'s last span is not the CRC: %+v", spans)
	}
	if crc := spans[len(spans)-1]; crc.Size != 2 {
		t.Errorf("Layout()'s CRC span size = %d, want 2", crc.Size)
	}
}

// 6. Disabling CRC removes it cleanly and returns its bytes to the budget.
func TestDesignerDisablingCRCRemovesItCleanly(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: crc8MaximDOW}
	d.schema.TotalSize = d.schema.FieldsSize() + d.schema.CRCSize()

	fieldsSize := d.schema.FieldsSize()
	remainingBefore := d.schema.Remaining()

	d.cursor = d.checksumRow()
	d.activateRow()
	if d.mode != dmCRCPreset {
		t.Fatalf("mode = %v, want dmCRCPreset", d.mode)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}) // 'n' disables the checksum

	if d.mode != dmBrowse {
		t.Fatalf("mode = %v, want dmBrowse after disabling", d.mode)
	}
	if size := d.schema.CRCSize(); size != 0 {
		t.Fatalf("CRCSize = %d, want 0 after disabling", size)
	}
	if _, _, ok := d.schema.CRCOffset(); ok {
		t.Error("CRCOffset still reports a checksum after disabling")
	}
	if d.schema.FieldsSize() != fieldsSize {
		t.Error("disabling the checksum must not touch normal field bytes")
	}
	if got, want := d.schema.Remaining(), remainingBefore+1; got != want {
		t.Errorf("Remaining after disabling = %d, want %d (the reserved CRC byte returned to the budget)", got, want)
	}
	// The row stays put (and last) so the checksum can be re-enabled.
	if d.checksumRow() != d.rowCount()-1 {
		t.Errorf("checksum row not last after disabling: %d vs %d", d.checksumRow(), d.rowCount()-1)
	}
}

// 7. Designer row order matches the packet diagram order.
func TestDesignerRowOrderMatchesDiagramOrder(t *testing.T) {
	m := newDesignerTestModel(t)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: crc8MaximDOW}
	d.schema.TotalSize = d.schema.FieldsSize() + d.schema.CRCSize()

	out := m.viewDesigner()

	// The row list, read straight off the view, must name each field (in
	// schema order) and then CRC — exactly what Schema.Layout() hands the
	// diagram immediately below it.
	var wantOrder []string
	for _, sp := range d.schema.Layout() {
		switch sp.Kind {
		case packet.SpanField:
			wantOrder = append(wantOrder, sp.Name)
		case packet.SpanCRC:
			wantOrder = append(wantOrder, "CRC")
		}
	}
	if len(wantOrder) != len(d.schema.Fields)+1 {
		t.Fatalf("test setup: want %d ordered labels (fields + CRC), got %v", len(d.schema.Fields)+1, wantOrder)
	}
	last := -1
	for _, label := range wantOrder {
		pos := strings.Index(out, label)
		if pos < 0 {
			t.Fatalf("label %q not found in designer view:\n%s", label, out)
		}
		if pos <= last {
			t.Errorf("row order mismatch: %q at %d does not come after the previous row at %d\n%s", label, pos, last, out)
		}
		last = pos
	}

	// The diagram itself (rendered standalone, matching what viewDesigner
	// embeds) must show the same field-then-CRC order.
	diagram := RenderDiagram(d.schema, DiagramOptions{Width: 100, Selected: -1})
	lastPos := -1
	for _, f := range d.schema.Fields {
		pos := strings.Index(diagram, f.Name)
		if pos < 0 {
			t.Fatalf("field %q missing from diagram:\n%s", f.Name, diagram)
		}
		if pos <= lastPos {
			t.Errorf("diagram field order mismatch at %q", f.Name)
		}
		lastPos = pos
	}
	crcPos := strings.Index(diagram, d.schema.Checksum.AlgorithmName())
	if crcPos < 0 {
		crcPos = strings.Index(diagram, "CRC")
	}
	if crcPos <= lastPos {
		t.Error("diagram's CRC cell does not come after all field cells")
	}
}

// 8. Narrow TUI rendering still behaves correctly.
func TestDesignerNarrowWidthKeepsTailChecksumInvariant(t *testing.T) {
	m := newDesignerTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 24, Height: 20})
	m = next.(*model)
	d := &m.designer
	d.schema = baseDesignerSchema()
	d.schema.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: crc8MaximDOW}
	d.schema.TotalSize = d.schema.FieldsSize() + d.schema.CRCSize()

	out := m.viewDesigner()
	if out == "" {
		t.Fatal("narrow designer view rendered empty")
	}
	if !strings.Contains(out, "CRC") {
		t.Error("narrow designer view lost the checksum row/cell entirely")
	}
	if d.checksumRow() != d.rowCount()-1 {
		t.Errorf("checksum row not last at narrow width: %d vs %d", d.checksumRow(), d.rowCount()-1)
	}

	// A field-mutating action must still respect the invariant at narrow
	// width — this isn't just a rendering concern.
	d.cursor = 1
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	off, _, ok := d.schema.CRCOffset()
	if !ok || off != d.schema.FieldsSize() {
		t.Errorf("CRC not at tail after duplicating at narrow width: off=%d fieldsSize=%d", off, d.schema.FieldsSize())
	}
}
