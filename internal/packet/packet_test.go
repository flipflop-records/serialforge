package packet

import (
	"bytes"
	"testing"

	"github.com/vtemnyakov/serialforge/internal/checksum"
)

// demoSchema mirrors the example protocol from the product spec: a
// 14-byte packet, HEADER/COMMAND/ADDRESS/DATA/RESERVED, CRC-8/MAXIM-DOW
// tail.
func demoSchema() Schema {
	return Schema{
		Name:      "demo",
		TotalSize: 14,
		Fields: []Field{
			{Name: "HEADER", Size: 2, Format: FormatHex},
			{Name: "COMMAND", Size: 1, Format: FormatUint},
			{Name: "ADDRESS", Size: 4, Endianness: BigEndian, Format: FormatHex},
			{Name: "DATA", Size: 4, Endianness: BigEndian, Format: FormatHex},
			{Name: "RESERVED", Size: 2, Format: FormatRaw},
		},
		Checksum: checksum.Definition{
			Mode:   checksum.ModePreset,
			Preset: "CRC-8/MAXIM-DOW",
		},
	}
}

func TestLayoutFieldsExactlyFillPacket(t *testing.T) {
	s := demoSchema()
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if s.Remaining() != 0 {
		t.Errorf("Remaining() = %d, want 0", s.Remaining())
	}
	spans := s.Layout()
	for _, sp := range spans {
		if sp.Kind == SpanUnallocated {
			t.Errorf("unexpected unallocated span in a fully-allocated schema: %+v", sp)
		}
	}
	// 5 fields + 1 CRC span, no unallocated span.
	if len(spans) != 6 {
		t.Fatalf("Layout() has %d spans, want 6: %+v", len(spans), spans)
	}
	crc := spans[5]
	if crc.Kind != SpanCRC || crc.Offset != 13 || crc.Size != 1 {
		t.Errorf("CRC span = %+v, want offset 13 size 1", crc)
	}
}

func TestLayoutFieldsExceedPacket(t *testing.T) {
	s := demoSchema()
	s.TotalSize = 10 // too small for 13 field bytes + 1 CRC byte
	err := s.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error for over-allocated schema")
	}
	t.Logf("got expected error: %v", err)
}

func TestLayoutRemainingBytes(t *testing.T) {
	s := demoSchema()
	s.TotalSize = 20 // 6 bytes left over
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error: under-allocated schema must not validate")
	}
	if got := s.Remaining(); got != 6 {
		t.Errorf("Remaining() = %d, want 6", got)
	}
	spans := s.Layout()
	last := spans[len(spans)-1]
	if last.Kind != SpanUnallocated || last.Size != 6 {
		t.Errorf("last span = %+v, want unallocated size 6", last)
	}
}

func TestCRCReservationShrinksAvailableFieldSpace(t *testing.T) {
	// Spec section 10: total=14, CRC-8 enabled => 13 bytes for fields, not
	// 14+1. A schema with 14 bytes of fields plus CRC-8 must NOT validate.
	s := Schema{
		TotalSize: 14,
		Fields:    []Field{{Name: "PAYLOAD", Size: 14}},
		Checksum:  checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8"},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error: CRC must be carved out of TotalSize, not added on top")
	}
	s.Fields[0].Size = 13
	if err := s.Validate(); err != nil {
		t.Fatalf("13 fields + 1 CRC byte in a 14-byte packet should validate: %v", err)
	}
}

func TestFieldReorderPreservesOffsets(t *testing.T) {
	s := demoSchema()
	// swap ADDRESS and DATA
	s.Fields[2], s.Fields[3] = s.Fields[3], s.Fields[2]
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	spans := s.Layout()
	if spans[2].Name != "DATA" || spans[2].Offset != 3 {
		t.Errorf("spans[2] = %+v, want DATA at offset 3", spans[2])
	}
	if spans[3].Name != "ADDRESS" || spans[3].Offset != 7 {
		t.Errorf("spans[3] = %+v, want ADDRESS at offset 7", spans[3])
	}
}

func TestSerializeDecodeRoundTrip(t *testing.T) {
	s := demoSchema()

	header, _ := EncodeUint(s.Fields[0], 0xAA55)
	command, _ := EncodeUint(s.Fields[1], 0x02)
	address, _ := EncodeUint(s.Fields[2], 0x00C017FF)
	data, _ := EncodeUint(s.Fields[3], 0xFFFF0100)
	reserved := []byte{0x00, 0x00}

	values := Values{
		"HEADER":   header,
		"COMMAND":  command,
		"ADDRESS":  address,
		"DATA":     data,
		"RESERVED": reserved,
	}

	raw, crc, err := Serialize(s, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if len(raw) != 14 {
		t.Fatalf("raw len = %d, want 14", len(raw))
	}
	if crc == nil || !crc.Valid {
		t.Fatalf("CRC result = %+v, want a valid AUTO crc", crc)
	}

	want := append([]byte{0xAA, 0x55, 0x02, 0x00, 0xC0, 0x17, 0xFF, 0xFF, 0xFF, 0x01, 0x00, 0x00, 0x00}, byte(crc.Calculated))
	if !bytes.Equal(raw, want) {
		t.Fatalf("raw = % X, want % X", raw, want)
	}

	pkt, err := Decode(s, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if pkt.CRC == nil || !pkt.CRC.Valid {
		t.Fatalf("decoded CRC = %+v, want valid", pkt.CRC)
	}
	byName := map[string]FieldValue{}
	for _, fv := range pkt.Fields {
		byName[fv.Field.Name] = fv
	}
	if byName["HEADER"].Uint != 0xAA55 {
		t.Errorf("HEADER = 0x%X, want 0xAA55", byName["HEADER"].Uint)
	}
	if byName["ADDRESS"].Uint != 0x00C017FF {
		t.Errorf("ADDRESS = 0x%X, want 0x00C017FF", byName["ADDRESS"].Uint)
	}
	if byName["COMMAND"].Uint != 0x02 {
		t.Errorf("COMMAND = 0x%X, want 0x02", byName["COMMAND"].Uint)
	}
}

func TestDecodeDetectsCRCFailure(t *testing.T) {
	s := demoSchema()
	values := Values{
		"HEADER":   {0xAA, 0x55},
		"COMMAND":  {0x02},
		"ADDRESS":  {0x00, 0xC0, 0x17, 0xFF},
		"DATA":     {0xFF, 0xFF, 0x01, 0x00},
		"RESERVED": {0x00, 0x00},
	}
	raw, _, err := Serialize(s, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	corrupt := append([]byte(nil), raw...)
	corrupt[len(corrupt)-1] ^= 0xFF // flip the CRC byte

	pkt, err := Decode(s, corrupt)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if pkt.CRC.Valid {
		t.Fatal("CRC.Valid = true for a corrupted CRC byte, want false")
	}
	if pkt.CRC.Received == pkt.CRC.Calculated {
		t.Fatal("Received should differ from Calculated after corruption")
	}
}

func TestManualCRCOverride(t *testing.T) {
	s := demoSchema()
	values := Values{
		"HEADER":   {0xAA, 0x55},
		"COMMAND":  {0x02},
		"ADDRESS":  {0x00, 0xC0, 0x17, 0xFF},
		"DATA":     {0xFF, 0xFF, 0x01, 0x00},
		"RESERVED": {0x00, 0x00},
	}
	bad := uint64(0x00)
	raw, crc, err := Serialize(s, values, &bad)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if !crc.Overridden {
		t.Error("Overridden = false, want true when the sent CRC differs from AUTO")
	}
	if crc.Received != 0x00 {
		t.Errorf("Received = 0x%X, want the manual override 0x00", crc.Received)
	}
	if raw[len(raw)-1] != 0x00 {
		t.Errorf("last byte = 0x%X, want the overridden CRC byte 0x00", raw[len(raw)-1])
	}
}

// TestManualCRCOverrideMatchingCalculated covers the case a UI must not
// conflate with AUTO: the user explicitly typed an override, but it
// happens to equal what AUTO would have sent anyway. Manual must stay true
// (it answers "did the sender type this in", not "does it differ") while
// Overridden — "did the override actually change the byte" — is false.
func TestManualCRCOverrideMatchingCalculated(t *testing.T) {
	s := demoSchema()
	values := Values{
		"HEADER":   {0xAA, 0x55},
		"COMMAND":  {0x02},
		"ADDRESS":  {0x00, 0xC0, 0x17, 0xFF},
		"DATA":     {0xFF, 0xFF, 0x01, 0x00},
		"RESERVED": {0x00, 0x00},
	}
	_, auto, err := Serialize(s, values, nil)
	if err != nil {
		t.Fatalf("Serialize (auto): %v", err)
	}
	matching := auto.Calculated
	_, crc, err := Serialize(s, values, &matching)
	if err != nil {
		t.Fatalf("Serialize (manual, matching): %v", err)
	}
	if !crc.Manual {
		t.Error("Manual = false, want true: crcOverride was explicitly supplied")
	}
	if crc.Overridden {
		t.Error("Overridden = true, want false: the override matches the calculated value")
	}
	if !crc.Valid {
		t.Error("Valid = false, want true: Received == Calculated")
	}
}

// TestAutoCRCIsNeverManual covers the AUTO path (crcOverride == nil):
// Manual and Overridden must both stay false, so TX display can tell AUTO
// and a coincidentally-matching OVERRIDE apart.
func TestAutoCRCIsNeverManual(t *testing.T) {
	s := demoSchema()
	values := Values{
		"HEADER":   {0xAA, 0x55},
		"COMMAND":  {0x02},
		"ADDRESS":  {0x00, 0xC0, 0x17, 0xFF},
		"DATA":     {0xFF, 0xFF, 0x01, 0x00},
		"RESERVED": {0x00, 0x00},
	}
	_, crc, err := Serialize(s, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if crc.Manual {
		t.Error("Manual = true, want false for an AUTO (nil crcOverride) build")
	}
	if crc.Overridden {
		t.Error("Overridden = true, want false for an AUTO build")
	}
}

func TestSerializeRejectsMissingField(t *testing.T) {
	s := demoSchema()
	values := Values{"HEADER": {0xAA, 0x55}}
	if _, _, err := Serialize(s, values, nil); err == nil {
		t.Fatal("Serialize() = nil error, want error for missing fields")
	}
}

func TestSerializeRejectsWrongSizeValue(t *testing.T) {
	s := demoSchema()
	values := Values{
		"HEADER":   {0xAA}, // wrong size: HEADER is 2 bytes
		"COMMAND":  {0x02},
		"ADDRESS":  {0x00, 0xC0, 0x17, 0xFF},
		"DATA":     {0xFF, 0xFF, 0x01, 0x00},
		"RESERVED": {0x00, 0x00},
	}
	if _, _, err := Serialize(s, values, nil); err == nil {
		t.Fatal("Serialize() = nil error, want error for wrong-size field value")
	}
}

func TestEncodeUintRejectsOverflow(t *testing.T) {
	f := Field{Name: "CMD", Size: 1}
	if _, err := EncodeUint(f, 256); err == nil {
		t.Fatal("EncodeUint(256) into a 1-byte field should error")
	}
	if _, err := EncodeUint(f, 255); err != nil {
		t.Fatalf("EncodeUint(255) into a 1-byte field: %v", err)
	}
}

func TestLittleEndianRoundTrip(t *testing.T) {
	f := Field{Name: "LE32", Size: 4, Endianness: LittleEndian}
	raw, err := EncodeUint(f, 0x01020304)
	if err != nil {
		t.Fatalf("EncodeUint: %v", err)
	}
	want := []byte{0x04, 0x03, 0x02, 0x01}
	if !bytes.Equal(raw, want) {
		t.Fatalf("raw = % X, want % X", raw, want)
	}
	got, ok := DecodeUint(f, raw)
	if !ok || got != 0x01020304 {
		t.Fatalf("DecodeUint = 0x%X, %v, want 0x01020304, true", got, ok)
	}
}

func TestNoChecksumSchema(t *testing.T) {
	s := Schema{
		TotalSize: 4,
		Fields:    []Field{{Name: "A", Size: 2}, {Name: "B", Size: 2}},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	raw, crc, err := Serialize(s, Values{"A": {1, 2}, "B": {3, 4}}, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if crc != nil {
		t.Errorf("CRCResult = %+v, want nil for a checksum-less schema", crc)
	}
	pkt, err := Decode(s, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if pkt.CRC != nil {
		t.Error("decoded CRC should be nil for a checksum-less schema")
	}
}

func TestValidateRejectsDuplicateFieldNames(t *testing.T) {
	s := Schema{
		TotalSize: 4,
		Fields:    []Field{{Name: "A", Size: 2}, {Name: "A", Size: 2}},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for duplicate field names")
	}
}

func TestValidateRejectsZeroSizeField(t *testing.T) {
	s := Schema{
		TotalSize: 4,
		Fields:    []Field{{Name: "A", Size: 0}, {Name: "B", Size: 4}},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for a zero-size field")
	}
}

func TestNonByteAlignedCRCWidthRejected(t *testing.T) {
	s := Schema{
		TotalSize: 4,
		Fields:    []Field{{Name: "A", Size: 3}},
		Checksum: checksum.Definition{
			Mode:   checksum.ModeCustom,
			Custom: checksum.Params{Width: 8, Poly: 0x07},
		},
	}
	// 3 field bytes + 1 CRC byte = 4, byte-aligned CRC: should validate.
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	s := demoSchema()
	c := s.Clone()
	c.Fields[0].Name = "CHANGED"
	if s.Fields[0].Name == "CHANGED" {
		t.Fatal("Clone() shares underlying field storage with the original")
	}
}
