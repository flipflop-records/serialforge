package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
)

// txTestSchema mirrors newTestModel's demo schema (internal/tui/model_test.go)
// — a 14-byte packet with a CRC-8/MAXIM-DOW tail — reused here so field
// names line up when driving m.tx.values directly.
func txTestSchema() packet.Schema {
	return packet.Schema{
		Name:      "demo",
		TotalSize: 14,
		Fields: []packet.Field{
			{Name: "header", Size: 2, Format: packet.FormatHex},
			{Name: "command", Size: 1, Format: packet.FormatUint},
			{Name: "address", Size: 4, Format: packet.FormatHex},
			{Name: "value", Size: 4, Format: packet.FormatHex},
			{Name: "reserved", Size: 2, Format: packet.FormatRaw},
		},
		Checksum: checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/MAXIM-DOW"},
	}
}

func fillTXValues(t *txState) {
	t.values = map[string]string{
		"header":   "AA55",
		"command":  "02",
		"address":  "00C017FF",
		"value":    "FFFF0100",
		"reserved": "0000",
	}
}

// TestViewTXCRCLineAutoShowsAlgorithmAndValue covers the AUTO case: the
// field-list CRC row must name the configured algorithm, say AUTO, and
// show the actual calculated byte — never PASS/FAIL (see
// TestRenderDiagramTXAutoNeverShowsPassFail for the diagram-cell side of
// the same rule).
func TestViewTXCRCLineAutoShowsAlgorithmAndValue(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabPackets
	m.packetsView = packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	fillTXValues(&m.tx)

	out := m.viewTX()
	if !strings.Contains(out, "CRC-8/MAXIM-DOW") {
		t.Errorf("expected algorithm name CRC-8/MAXIM-DOW in output:\n%s", out)
	}
	if !strings.Contains(out, "AUTO") {
		t.Errorf("expected AUTO mode in output:\n%s", out)
	}
	if strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("TX Builder must never show PASS/FAIL, got:\n%s", out)
	}
	// The actual transmitted CRC byte for this exact field content, computed
	// independently via Serialize, must appear literally in the view.
	values := packet.Values{}
	for _, f := range sc.Fields {
		raw, err := decodeHexTUI(m.tx.values[f.Name])
		if err != nil {
			t.Fatalf("decodeHexTUI(%s): %v", f.Name, err)
		}
		values[f.Name] = raw
	}
	_, crc, err := packet.Serialize(sc, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	want := crcHexValue(crc.Width, crc.Received)
	if !strings.Contains(out, want) {
		t.Errorf("expected calculated CRC value %q in output:\n%s", want, out)
	}
}

// TestViewTXCRCLineOverrideShowsOverrideAndMismatch covers a manual
// override that differs from AUTO: the row must say OVERRIDE and surface
// the calculated value too, so the user can see the override actually
// changed the byte.
func TestViewTXCRCLineOverrideShowsOverrideAndMismatch(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabPackets
	m.packetsView = packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	fillTXValues(&m.tx)
	m.tx.crcOverride = "42"

	out := m.viewTX()
	if !strings.Contains(out, "OVERRIDE") {
		t.Errorf("expected OVERRIDE mode in output:\n%s", out)
	}
	if strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("TX Builder must never show PASS/FAIL, got:\n%s", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("expected overridden value 42 in output:\n%s", out)
	}
	if !strings.Contains(out, "calculated") {
		t.Errorf("expected a mismatch note naming the calculated value, got:\n%s", out)
	}
}

// TestViewTXCRCLineOmittedWithoutChecksum covers a schema with no checksum
// configured at all — the field-list must not print a fictitious "CRC" row.
func TestViewTXCRCLineOmittedWithoutChecksum(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabPackets
	m.packetsView = packetsTX
	sc := txTestSchema()
	sc.Checksum = checksum.Definition{}
	sc.Fields[len(sc.Fields)-1].Size += 1 // keep TotalSize allocated without the CRC byte
	m.tx.schema = &sc
	fillTXValues(&m.tx)

	out := m.viewTX()
	// The "c set/clear CRC override" key hint always mentions CRC by name;
	// what must be absent is the field-list *row* itself — plain "  CRC"
	// (unlike field rows, it's never marker-styled — see viewTX).
	if strings.Contains(out, "  CRC ") || strings.Contains(out, "  CRC\t") {
		t.Errorf("expected no CRC field-list row for a checksum-less schema, got:\n%s", out)
	}
}

// TestViewRXCRCLineShowsRXCalcAndStatus covers the RX Inspector's explicit
// breakdown: CRC RX / CALC / PASS-FAIL, spelled out separately from the
// diagram's own PASS/FAIL cell.
func TestViewRXCRCLineShowsRXCalcAndStatus(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabPackets
	m.packetsView = packetsRX
	sc := txTestSchema()
	m.activeSchema = &sc

	values := packet.Values{
		"header":   {0xAA, 0x55},
		"command":  {0x02},
		"address":  {0x00, 0xC0, 0x17, 0xFF},
		"value":    {0xFF, 0xFF, 0x01, 0x00},
		"reserved": {0x00, 0x00},
	}
	raw, _, err := packet.Serialize(sc, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF // corrupt the CRC byte so RX vs CALC actually differ
	pkt, err := packet.Decode(sc, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	m.rx.history = []*packet.Packet{pkt}
	m.rx.cursor = 0

	out := m.viewRX()
	if !strings.Contains(out, "CRC RX") {
		t.Errorf("expected a \"CRC RX\" line in output:\n%s", out)
	}
	if !strings.Contains(out, "CALC") {
		t.Errorf("expected a \"CALC\" line in output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL for a corrupted CRC byte:\n%s", out)
	}
	rxVal := crcHexValue(pkt.CRC.Width, pkt.CRC.Received)
	calcVal := crcHexValue(pkt.CRC.Width, pkt.CRC.Calculated)
	if !strings.Contains(out, rxVal) || !strings.Contains(out, calcVal) {
		t.Errorf("expected RX value %q and CALC value %q in output:\n%s", rxVal, calcVal, out)
	}
}

// --- bounded field/CRC-override hex editing ---------------------------------
//
// TX Builder edits every field as a hex string regardless of its declared
// packet.Format (uint/int/ascii/raw are Designer-only metadata today, not
// separate TX Builder editing modes — see boundedinput.go's doc comment),
// so the one bound that applies is digit count: a field can never take more
// than 2 hex digits per declared byte, and a manual CRC override can never
// take more than 2 hex digits per byte of the active checksum's own width.

// openTXFieldEdit points fieldCursor at the named field and opens its
// editor via the real key-dispatch path (m.handleKey), matching how a user
// actually reaches txEditField.
func openTXFieldEdit(t *testing.T, m *model, fieldName string) {
	t.Helper()
	for i, f := range m.tx.schema.Fields {
		if f.Name == fieldName {
			m.tx.fieldCursor = i
			break
		}
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.tx.mode != txEditField {
		t.Fatalf("enter should open the field editor, mode = %v", m.tx.mode)
	}
}

func TestTXFieldHexEditAllowsExactlyTwoDigitsPerByte(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "command") // 1-byte field

	typeString(m, "FF")
	if m.tx.editBuf != "FF" {
		t.Fatalf("editBuf = %q, want %q (exactly 2 digits for a 1-byte field)", m.tx.editBuf, "FF")
	}
	typeString(m, "F") // a third digit must never be inserted
	if m.tx.editBuf != "FF" {
		t.Fatalf("editBuf after a third digit = %q, want unchanged %q", m.tx.editBuf, "FF")
	}
}

func TestTXFieldHexEditTwoByteFieldAllowsFourDigits(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "header") // 2-byte field

	typeString(m, "AABB")
	if m.tx.editBuf != "AABB" {
		t.Fatalf("editBuf = %q, want %q", m.tx.editBuf, "AABB")
	}
	typeString(m, "C")
	if m.tx.editBuf != "AABB" {
		t.Fatalf("editBuf after a 5th digit = %q, want unchanged %q", m.tx.editBuf, "AABB")
	}
}

// Formatted hex spacing (a space between byte pairs, which the editor
// already tolerates and cleanHexTUI strips at submit) must not let more
// semantic hex digits through than the field allows.
func TestTXFieldHexEditSpacingCannotBypassDigitCount(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "header") // 2-byte field, 4 digits max

	typeString(m, "AA BB") // 4 semantic digits + a space
	if got := countHexDigits(m.tx.editBuf); got != 4 {
		t.Fatalf("hex digit count = %d, want 4 (buffer: %q)", got, m.tx.editBuf)
	}
	typeString(m, "C") // 5th digit — must still be rejected
	if got := countHexDigits(m.tx.editBuf); got != 4 {
		t.Fatalf("hex digit count after a 5th digit = %d, want unchanged 4 (buffer: %q)", got, m.tx.editBuf)
	}
}

// A real paste delivers every pasted character in one KeyMsg (bubbletea's
// bracketed paste, enabled by default) — the whole batch must still be
// checked digit by digit, never accepted wholesale.
func TestTXFieldHexEditPasteCannotBypassDigitCount(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "header") // 2-byte field, 4 digits max

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("AABBCCDD"), Paste: true})
	if m.tx.editBuf != "AABB" {
		t.Fatalf("editBuf after pasting an 8-digit value = %q, want only the valid prefix %q", m.tx.editBuf, "AABB")
	}
}

// A rejected keystroke must never disturb what's already in the buffer —
// backspace/continued editing after a rejection behaves exactly as if the
// rejection never happened.
func TestTXFieldHexEditRejectionLeavesBufferEditable(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "command") // 1-byte field

	typeString(m, "FF")
	typeString(m, "9") // rejected
	pressKey(m, tea.KeyBackspace)
	if m.tx.editBuf != "F" {
		t.Fatalf("editBuf after a rejected digit then backspace = %q, want %q", m.tx.editBuf, "F")
	}
	typeString(m, "0")
	if m.tx.editBuf != "F0" {
		t.Fatalf("editBuf = %q, want %q", m.tx.editBuf, "F0")
	}
}

// Submit-time validation remains present: an incomplete value (fewer than
// f.Size*2 digits) is still rejected at Enter — the input-time bound only
// ever prevents *too many* digits, never requires the submit-time exact-
// length check to be relaxed.
func TestTXFieldSubmitStillRequiresExactLength(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "header") // 2-byte field

	typeString(m, "AA") // only 2 of 4 digits
	pressKey(m, tea.KeyEnter)
	if m.tx.mode != txEditField {
		t.Fatalf("an incomplete value should not be accepted, mode = %v", m.tx.mode)
	}
	if m.tx.message == "" {
		t.Error("expected a validation message for an incomplete field value")
	}

	typeString(m, "BB") // now exactly 4
	pressKey(m, tea.KeyEnter)
	if m.tx.mode != txBrowse {
		t.Fatalf("a complete value should submit, mode = %v", m.tx.mode)
	}
	if m.tx.values["header"] != "AABB" {
		t.Errorf("values[header] = %q, want %q", m.tx.values["header"], "AABB")
	}
}

// CRC override: bounded to the active checksum's own reserved width.
func TestTXCRCOverrideBoundedToChecksumWidth(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema() // CRC-8/MAXIM-DOW: 1 byte
	m.tx.schema = &sc

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.tx.mode != txEditCRC {
		t.Fatalf("'c' should open the CRC override editor, mode = %v", m.tx.mode)
	}
	typeString(m, "FF")
	if m.tx.editBuf != "FF" {
		t.Fatalf("editBuf = %q, want %q (2 digits for a 1-byte CRC)", m.tx.editBuf, "FF")
	}
	typeString(m, "F") // a 3rd digit must never be inserted
	if m.tx.editBuf != "FF" {
		t.Fatalf("editBuf after a 3rd digit = %q, want unchanged %q", m.tx.editBuf, "FF")
	}
}

// Widening the active CRC (e.g. picking a 16-bit preset) immediately
// widens the override's own digit budget — derived live from the schema,
// not cached from when the editor opened.
func TestTXCRCOverrideWidthChangesWithChecksumWidth(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	sc := txTestSchema()
	sc.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-16/ARC"} // 2 bytes
	sc.Fields[len(sc.Fields)-1].Size -= 1                                              // keep TotalSize allocated (CRC grew by 1 byte)
	m.tx.schema = &sc

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	typeString(m, "ABCD")
	if m.tx.editBuf != "ABCD" {
		t.Fatalf("editBuf = %q, want %q (4 digits for a 2-byte CRC)", m.tx.editBuf, "ABCD")
	}
	typeString(m, "E") // a 5th digit must never be inserted
	if m.tx.editBuf != "ABCD" {
		t.Fatalf("editBuf after a 5th digit = %q, want unchanged %q", m.tx.editBuf, "ABCD")
	}
}

// Narrow TUI rendering remains clean with the new capacity indicator.
func TestTXFieldEditRendersAtNarrowWidth(t *testing.T) {
	m := newTestModel(t)
	m.tab, m.packetsView = tabPackets, packetsTX
	next, _ := m.Update(tea.WindowSizeMsg{Width: 28, Height: 20})
	m = next.(*model)
	sc := txTestSchema()
	m.tx.schema = &sc
	openTXFieldEdit(t, m, "command")

	out := m.viewTX()
	if out == "" {
		t.Fatal("narrow TX Builder edit view rendered empty")
	}
	if !strings.Contains(out, "0/2 hex digits") {
		t.Errorf("expected a digit-count indicator, got:\n%s", out)
	}
}

// Loading a Saved Packet into TX Builder must use the identical bounded
// editing path — no separate Saved Packet editor with different limits.
// savedDemoPacket (savedpackets_test.go) references the "demo" protocol
// newTestModel already registers, with the exact same field shape as
// txTestSchema above.
func TestSavedPacketLoadedIntoTXUsesSameFieldEditBound(t *testing.T) {
	m := newTestModel(t)
	sp := savedDemoPacket("get-status", "")
	_ = m.cfg.SavedPackets.Put(sp)

	m.tab, m.packetsView = tabPackets, packetsSaved
	spLoaded, ok := m.cfg.SavedPackets.Get("get-status")
	if !ok {
		t.Fatal("test setup: saved packet not found")
	}
	m.loadSavedPacketIntoTX(spLoaded)
	if m.packetsView != packetsTX || m.tx.schema == nil {
		t.Fatal("loadSavedPacketIntoTX should switch to TX Builder with a schema loaded")
	}

	openTXFieldEdit(t, m, "command") // 1-byte field, prefilled with the loaded value "02"
	backspaceN(m, len(m.tx.editBuf)) // clear the prefilled value, same convention every other form uses
	typeString(m, "FF9")             // 3rd digit must be rejected
	if m.tx.editBuf != "FF" {
		t.Fatalf("editBuf on a loaded Saved Packet = %q, want %q — same 2-digit-per-byte bound as a fresh TX session", m.tx.editBuf, "FF")
	}
}
