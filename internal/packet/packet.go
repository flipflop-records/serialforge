package packet

import (
	"fmt"
	"time"
)

// FieldValue is one decoded field: its schema definition, the exact raw
// wire bytes (always present and authoritative), and best-effort numeric/
// text interpretations for display. Uint/Int are only valid (UintOK/IntOK
// true) for fields up to 8 bytes — wider fields are still fully readable
// via Raw, just not as a single machine integer.
type FieldValue struct {
	Field  Field
	Raw    []byte
	Uint   uint64
	UintOK bool
	Int    int64
	IntOK  bool
	ASCII  string
}

// CRCResult carries both sides of a checksum comparison so RX inspection
// can show PASS/FAIL without recomputing, and TX preview can show a
// manually overridden CRC as clearly not what AUTO would have sent.
//
// Manual and Valid answer two different questions that TX display must not
// conflate: Manual is "did the sender type this value in" (the AUTO/OVERRIDE
// mode), while Valid is "does the transmitted value match what the
// algorithm alone would have computed" (only meaningful as a PASS/FAIL
// claim once bytes have actually arrived over the wire — see RX's use of
// Valid in Decode). A TX packet the user never overrode is Valid by
// construction (Received == Calculated); that is not the same fact as a
// device confirming the CRC it received, so TUI code must never print
// PASS/FAIL from a TX-built Packet — see ARCHITECTURE.md's CRC presentation
// invariants.
type CRCResult struct {
	Width      int
	Received   uint64 // the value present in the packet bytes (what's actually sent/was received)
	Calculated uint64 // what the schema's CRC algorithm computes over the coverage bytes
	Valid      bool   // Received == Calculated
	Manual     bool   // TX only: the sender explicitly supplied Received rather than accepting AUTO
	Overridden bool   // TX only: Manual and Received != Calculated (the override actually changed the byte)
}

// Direction distinguishes TX from RX packets in history/inspection views.
type Direction string

const (
	DirectionTX Direction = "tx"
	DirectionRX Direction = "rx"
)

// Packet is one fully materialized packet: the schema it was built or
// decoded against, the exact raw bytes, per-field decoded values, and the
// CRC comparison if the schema has a checksum. It is what the packet
// inspector, TX preview, and RX decoder all display.
type Packet struct {
	Schema    Schema
	Raw       []byte
	Fields    []FieldValue
	CRC       *CRCResult
	Direction Direction
	Timestamp time.Time
}

// coverageBytes extracts the byte range the CRC is computed over, per the
// schema's checksum.Coverage. CoverAllBeforeCRC (the default, and the only
// mode wired up for now — see checksum.Coverage's doc comment) covers every
// byte before the CRC field.
func coverageBytes(s Schema, raw []byte) ([]byte, error) {
	crcOff, _, ok := s.CRCOffset()
	if !ok {
		return nil, fmt.Errorf("packet: schema has no checksum enabled")
	}
	cov := s.Checksum.Coverage
	switch cov.Mode {
	case "", "all_before_crc":
		return raw[:crcOff], nil
	case "range":
		if cov.Start < 0 || cov.End > len(raw) || cov.Start > cov.End {
			return nil, fmt.Errorf("packet: checksum coverage range [%d,%d) is out of bounds for a %d-byte packet", cov.Start, cov.End, len(raw))
		}
		return raw[cov.Start:cov.End], nil
	default:
		return nil, fmt.Errorf("packet: unknown checksum coverage mode %q", cov.Mode)
	}
}

func decodeFieldValue(f Field, raw []byte) FieldValue {
	fv := FieldValue{Field: f, Raw: raw, ASCII: asciiOf(raw)}
	fv.Uint, fv.UintOK = DecodeUint(f, raw)
	fv.Int, fv.IntOK = DecodeInt(f, raw)
	return fv
}

func asciiOf(raw []byte) string {
	out := make([]byte, len(raw))
	for i, b := range raw {
		if b >= 0x20 && b < 0x7F {
			out[i] = b
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}
