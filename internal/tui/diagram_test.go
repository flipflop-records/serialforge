package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
)

func demoSchema() packet.Schema {
	return packet.Schema{
		Name:      "demo",
		TotalSize: 14,
		Fields: []packet.Field{
			{Name: "HEADER", Size: 2, Format: packet.FormatHex},
			{Name: "CMD", Size: 1, Format: packet.FormatUint},
			{Name: "ADDRESS", Size: 4, Format: packet.FormatHex},
			{Name: "DATA", Size: 4, Format: packet.FormatHex},
			{Name: "RSV", Size: 2, Format: packet.FormatRaw},
		},
		Checksum: checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/MAXIM-DOW"},
	}
}

func plainWidth(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		w := lipgloss.Width(line)
		if w > max {
			max = w
		}
	}
	return max
}

func TestRenderDiagramFitsWidth(t *testing.T) {
	sc := demoSchema()
	out := RenderDiagram(sc, DiagramOptions{Width: 80, Selected: -1})
	if out == "" {
		t.Fatal("RenderDiagram returned empty output")
	}
	if w := plainWidth(out); w > 80 {
		t.Errorf("rendered width %d exceeds requested 80", w)
	}
	for _, f := range sc.Fields {
		if !strings.Contains(out, f.Name) {
			t.Errorf("output missing field name %q:\n%s", f.Name, out)
		}
	}
}

func TestRenderDiagramNarrowWidthWraps(t *testing.T) {
	sc := demoSchema()
	out := RenderDiagram(sc, DiagramOptions{Width: 20, Selected: -1})
	// A 14-byte, 6-span (5 fields + CRC) packet cannot fit in 20 columns —
	// expect more than one "bytes N–M" row caption, i.e. multi-row wrap.
	if strings.Count(out, "bytes ") < 2 {
		t.Errorf("expected multiple wrapped rows at width 20, got:\n%s", out)
	}
	if w := plainWidth(out); w > 20 {
		t.Errorf("rendered width %d exceeds requested 20", w)
	}
}

func TestRenderDiagramEmptySchema(t *testing.T) {
	sc := packet.Schema{Name: "new", TotalSize: 32}
	out := RenderDiagram(sc, DiagramOptions{Width: 80, Selected: -1})
	if !strings.Contains(out, "UNALLOCATED") {
		t.Errorf("expected an UNALLOCATED span for a fieldless schema, got:\n%s", out)
	}
}

func TestRenderDiagramShowsValuesAndCRCStatus(t *testing.T) {
	sc := demoSchema()
	values := packet.Values{
		"HEADER":  {0xAA, 0x55},
		"CMD":     {0x02},
		"ADDRESS": {0x00, 0xC0, 0x17, 0xFF},
		"DATA":    {0xFF, 0xFF, 0x01, 0x00},
		"RSV":     {0x00, 0x00},
	}
	raw, crc, err := packet.Serialize(sc, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	pkt, err := packet.Decode(sc, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := RenderDiagram(sc, DiagramOptions{Width: 100, Selected: -1, Values: values, CRCResult: pkt.CRC})
	if !strings.Contains(out, "AA 55") {
		t.Errorf("expected HEADER value AA 55 in output:\n%s", out)
	}
	if !strings.Contains(out, "PASS") {
		t.Errorf("expected CRC PASS in output:\n%s", out)
	}
	_ = crc
}

// TestRenderDiagramTXAutoNeverShowsPassFail pins down the core semantic
// distinction this behavior depends on: a TX-built packet's CRC cell (mode
// CRCDisplayAuto) must show the actual value and AUTO/OVERRIDE, never
// PASS/FAIL — internal arithmetic consistency is not a claim about bytes
// a device confirmed receiving.
func TestRenderDiagramTXAutoNeverShowsPassFail(t *testing.T) {
	sc := demoSchema()
	values := packet.Values{
		"HEADER":  {0xAA, 0x55},
		"CMD":     {0x02},
		"ADDRESS": {0x00, 0xC0, 0x17, 0xFF},
		"DATA":    {0xFF, 0xFF, 0x01, 0x00},
		"RSV":     {0x00, 0x00},
	}
	_, crc, err := packet.Serialize(sc, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	out := RenderDiagram(sc, DiagramOptions{Width: 100, Selected: -1, Values: values, CRCResult: crc, CRCDisplay: CRCDisplayAuto})
	if strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("CRCDisplayAuto must never show PASS/FAIL, got:\n%s", out)
	}
	if !strings.Contains(out, "AUTO") {
		t.Errorf("expected AUTO mode in output:\n%s", out)
	}
	wantValue := crcHexValue(crc.Width, crc.Received)
	if !strings.Contains(out, wantValue) {
		t.Errorf("expected CRC value %q in output:\n%s", wantValue, out)
	}
}

// TestRenderDiagramTXOverrideShowsOverrideMode covers the manually
// overridden case: the cell must say OVERRIDE (not AUTO) and show the
// overridden byte, still with no PASS/FAIL.
func TestRenderDiagramTXOverrideShowsOverrideMode(t *testing.T) {
	sc := demoSchema()
	values := packet.Values{
		"HEADER":  {0xAA, 0x55},
		"CMD":     {0x02},
		"ADDRESS": {0x00, 0xC0, 0x17, 0xFF},
		"DATA":    {0xFF, 0xFF, 0x01, 0x00},
		"RSV":     {0x00, 0x00},
	}
	override := uint64(0x42)
	_, crc, err := packet.Serialize(sc, values, &override)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if !crc.Manual {
		t.Fatal("expected Manual=true for an overridden build")
	}
	// Generous width: the CRC cell only gets a byte-proportional share of
	// the row, and this test is about the OVERRIDE word surviving intact,
	// not about narrow-width abbreviation (see TestCRCCellLabelPicksLongestFitting
	// and TestRenderDiagramNarrowCRCCellAbbreviates for that).
	out := RenderDiagram(sc, DiagramOptions{Width: 200, Selected: -1, Values: values, CRCResult: crc, CRCDisplay: CRCDisplayAuto})
	if !strings.Contains(out, "OVERRIDE") {
		t.Errorf("expected OVERRIDE mode in output:\n%s", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("expected overridden value 42 in output:\n%s", out)
	}
	if strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("CRCDisplayAuto must never show PASS/FAIL, got:\n%s", out)
	}
}

// TestRenderDiagramRXComparePassFail covers the RX/decode reading
// (CRCDisplayCompare, the zero value): PASS/FAIL from Received vs.
// Calculated, matching the existing decode-based test's expectation.
func TestRenderDiagramRXComparePassFail(t *testing.T) {
	sc := demoSchema()
	values := packet.Values{
		"HEADER":  {0xAA, 0x55},
		"CMD":     {0x02},
		"ADDRESS": {0x00, 0xC0, 0x17, 0xFF},
		"DATA":    {0xFF, 0xFF, 0x01, 0x00},
		"RSV":     {0x00, 0x00},
	}
	raw, _, err := packet.Serialize(sc, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF // corrupt the CRC byte
	pkt, err := packet.Decode(sc, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := RenderDiagram(sc, DiagramOptions{Width: 100, Selected: -1, Values: values, CRCResult: pkt.CRC, CRCDisplay: CRCDisplayCompare})
	if !strings.Contains(out, "FAIL") {
		t.Errorf("expected FAIL for a corrupted CRC byte:\n%s", out)
	}
	if strings.Contains(out, "AUTO") || strings.Contains(out, "OVERRIDE") {
		t.Errorf("CRCDisplayCompare must never show AUTO/OVERRIDE, got:\n%s", out)
	}
}

// TestCRCCellLabelPicksLongestFitting exercises crcCellLabel directly
// against exact widths, pinning down the "longest catalogued alias that
// still fits" selection the responsive packet-diagram cell depends on —
// see checksum.Definition.AlgorithmLabels for where the candidates
// themselves come from (CRC-8/MAXIM-DOW's aliases: CRC-8/MAXIM, DOW-CRC,
// 1-WIRE, in that declared order, lengths 11/7/6).
func TestCRCCellLabelPicksLongestFitting(t *testing.T) {
	def := checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/MAXIM-DOW"}
	cases := []struct {
		w    int
		want string
	}{
		{20, "CRC-8/MAXIM-DOW"}, // full canonical name fits
		{16, "CRC-8/MAXIM-DOW"}, // exactly fits
		{12, "CRC-8/MAXIM"},     // canonical (16) too long; longest alias that fits
		{10, "DOW-CRC"},         // "CRC-8/MAXIM" (11) no longer fits
		{6, "1-WIRE"},           // exactly the shortest alias
		{3, "1-WIRE"},           // nothing fits; falls back to the globally shortest candidate
	}
	for _, c := range cases {
		if got := crcCellLabel(def, c.w); got != c.want {
			t.Errorf("crcCellLabel(w=%d) = %q, want %q", c.w, got, c.want)
		}
	}
}

// TestCRCCellLabelCustomFallsBackToBareCRC covers a custom (non-catalogued)
// CRC narrowed past even "CRC-16" — it must still degrade to the generic
// "CRC" rather than an empty or truncated-mid-word label.
func TestCRCCellLabelCustomFallsBackToBareCRC(t *testing.T) {
	def := checksum.Definition{Mode: checksum.ModeCustom, Custom: checksum.Params{Width: 16}}
	if got := crcCellLabel(def, 4); got != "CRC" {
		t.Errorf("crcCellLabel(custom, w=4) = %q, want %q", got, "CRC")
	}
	if got := crcCellLabel(def, 100); got != "CUSTOM CRC-16" {
		t.Errorf("crcCellLabel(custom, w=100) = %q, want %q", got, "CUSTOM CRC-16")
	}
}

// TestRenderDiagramNarrowCRCCellAbbreviates is the integration-level
// counterpart: at a width too narrow for the full algorithm name, the
// rendered diagram must still contain a recognizable candidate name
// (crcCellLabel's job), not an empty cell or a layout that blows past the
// requested width.
func TestRenderDiagramNarrowCRCCellAbbreviates(t *testing.T) {
	sc := demoSchema()
	out := RenderDiagram(sc, DiagramOptions{Width: 30, Selected: -1})
	if w := plainWidth(out); w > 30 {
		t.Errorf("rendered width %d exceeds requested 30:\n%s", w, out)
	}
	labels := checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/MAXIM-DOW"}.AlgorithmLabels()
	found := false
	for _, l := range labels {
		if strings.Contains(out, l) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected one of the CRC algorithm's names %v to appear intact, got:\n%s", labels, out)
	}
}

// TestCRCAutoValueCellDegradesInTiers pins crcAutoValueCell's three tiers —
// full "<value> · MODE", compact "<value> MODE" (OVERRIDE shortened to
// OVR), then the bare value — so a cramped diagram cell shrinks to a still-
// legible shorter form instead of an ellipsis cutting a word in half (e.g.
// never "OVER…").
func TestCRCAutoValueCellDegradesInTiers(t *testing.T) {
	auto := packet.CRCResult{Width: 8, Received: 0x09, Calculated: 0x09}
	override := packet.CRCResult{Width: 8, Received: 0x42, Calculated: 0x09, Manual: true, Overridden: true}

	// crcAutoValueCell measures by rune, not byte — "·" (U+00B7) is 2 bytes
	// in UTF-8 but 1 rune, so widths below are rune counts (runeLen), not
	// Go's byte-counting len().
	runeLen := func(s string) int { return len([]rune(s)) }

	cases := []struct {
		name string
		r    packet.CRCResult
		w    int
		want string
	}{
		{"auto, generous width: full", auto, 20, "09 · AUTO"},
		{"auto, exactly the full form's width", auto, runeLen("09 · AUTO"), "09 · AUTO"},
		{"auto, full doesn't fit: compact", auto, runeLen("09 · AUTO") - 1, "09 AUTO"},
		{"auto, nothing fits: bare value", auto, 2, "09"},
		{"override, generous width: full", override, 20, "42 · OVERRIDE"},
		{"override, exactly the full form's width", override, runeLen("42 · OVERRIDE"), "42 · OVERRIDE"},
		{"override, full doesn't fit: compact OVR", override, runeLen("42 · OVERRIDE") - 1, "42 OVR"},
		{"override, exactly the compact form's width", override, runeLen("42 OVR"), "42 OVR"},
		{"override, nothing fits: bare value", override, 5, "42"},
	}
	for _, c := range cases {
		if got := crcAutoValueCell(c.r, c.w); got != c.want {
			t.Errorf("%s: crcAutoValueCell(w=%d) = %q, want %q", c.name, c.w, got, c.want)
		}
	}
}

func TestRenderDiagramSelectionHighlight(t *testing.T) {
	sc := demoSchema()
	out := RenderDiagram(sc, DiagramOptions{Width: 80, Selected: 1}) // CMD field
	if !strings.Contains(out, "▸CMD") {
		t.Errorf("expected the selected field to carry the ▸ marker, got:\n%s", out)
	}
}
