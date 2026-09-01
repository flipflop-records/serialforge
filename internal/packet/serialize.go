package packet

import "fmt"

// Values maps a field name to its exact wire bytes (already
// endianness-encoded — see EncodeUint/EncodeASCII). Serialize requires an
// entry, of exactly the field's declared size, for every field in the
// schema; there is no implicit zero-fill, because a silently-defaulted
// field is exactly the kind of hidden byte the product spec insists the
// engineer must never lose sight of.
type Values map[string][]byte

// Serialize builds the complete wire packet for schema s from values,
// computing (or, if crcOverride is non-nil, overriding) the CRC. It returns
// the raw bytes and a CRCResult describing both the calculated and the
// actually-sent CRC value; CRCResult is nil when the schema has no
// checksum. Serialize refuses an invalid schema (Validate) so a caller can
// never send bytes that don't match what the diagram is showing.
func Serialize(s Schema, values Values, crcOverride *uint64) ([]byte, *CRCResult, error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}
	buf := make([]byte, s.TotalSize)
	off := 0
	for _, f := range s.Fields {
		v, ok := values[f.Name]
		if !ok {
			return nil, nil, fmt.Errorf("packet: missing value for field %q", f.Name)
		}
		if len(v) != f.Size {
			return nil, nil, fmt.Errorf("packet: value for field %q is %d bytes, want %d", f.Name, len(v), f.Size)
		}
		copy(buf[off:off+f.Size], v)
		off += f.Size
	}

	crcOff, crcSize, hasCRC := s.CRCOffset()
	if !hasCRC {
		return buf, nil, nil
	}

	engine, err := s.Checksum.CRC()
	if err != nil {
		return nil, nil, fmt.Errorf("packet: checksum: %w", err)
	}
	cov, err := coverageBytes(s, buf)
	if err != nil {
		return nil, nil, err
	}
	calculated := engine.Compute(cov)

	final := calculated
	manual := crcOverride != nil
	if manual {
		final = *crcOverride
	}
	packed := packBytes(final, crcSize, s.Checksum.EffectiveEndianness())
	copy(buf[crcOff:crcOff+crcSize], packed)

	return buf, &CRCResult{
		Width:      engine.Params().Width,
		Received:   final,
		Calculated: calculated,
		Valid:      final == calculated,
		Manual:     manual,
		Overridden: manual && final != calculated,
	}, nil
}

// Build is Serialize plus assembling the full decoded Packet view (as if
// the bytes had just been received) — the shape the TX preview / packet
// inspector wants: raw bytes, per-field values, and the CRC comparison, all
// in one call.
func Build(s Schema, values Values, crcOverride *uint64) (*Packet, error) {
	raw, crc, err := Serialize(s, values, crcOverride)
	if err != nil {
		return nil, err
	}
	pkt, err := Decode(s, raw)
	if err != nil {
		return nil, err
	}
	pkt.CRC = crc // Decode recomputes CRC from raw; Serialize's result also carries Overridden.
	pkt.Direction = DirectionTX
	return pkt, nil
}
