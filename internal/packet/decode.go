package packet

import "fmt"

// Decode interprets raw as an instance of schema s: it must be exactly
// s.TotalSize bytes. Every field is decoded (raw bytes preserved plus
// best-effort numeric/ASCII views); if the schema has a checksum, its
// received and calculated values are both reported via Packet.CRC so a
// mismatch is a fact the caller can display, never a fact the decoder
// hides by refusing to decode.
func Decode(s Schema, raw []byte) (*Packet, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if len(raw) != s.TotalSize {
		return nil, fmt.Errorf("packet: got %d bytes, schema %q requires exactly %d", len(raw), s.Name, s.TotalSize)
	}

	pkt := &Packet{
		Schema:    s,
		Raw:       raw,
		Direction: DirectionRX,
	}

	off := 0
	for _, f := range s.Fields {
		pkt.Fields = append(pkt.Fields, decodeFieldValue(f, raw[off:off+f.Size]))
		off += f.Size
	}

	if crcOff, crcSize, ok := s.CRCOffset(); ok {
		engine, err := s.Checksum.CRC()
		if err != nil {
			return nil, fmt.Errorf("packet: checksum: %w", err)
		}
		cov, err := coverageBytes(s, raw)
		if err != nil {
			return nil, err
		}
		received := unpackBytes(raw[crcOff:crcOff+crcSize], s.Checksum.EffectiveEndianness())
		calculated := engine.Compute(cov)
		pkt.CRC = &CRCResult{
			Width:      engine.Params().Width,
			Received:   received,
			Calculated: calculated,
			Valid:      received == calculated,
		}
	}

	return pkt, nil
}
