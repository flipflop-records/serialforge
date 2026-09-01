package tui

import (
	"strings"
	"testing"

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
