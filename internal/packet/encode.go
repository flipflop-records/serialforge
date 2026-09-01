package packet

import "fmt"

// EncodeUint packs v into f.Size wire bytes using f's endianness. Returns an
// error if v does not fit in f.Size bytes (silently truncating a value the
// user typed would hide a mistake — the product invariant is that the
// engineer can always see and trust the exact bytes being sent).
func EncodeUint(f Field, v uint64) ([]byte, error) {
	if f.Size < 8 {
		limit := uint64(1) << uint(f.Size*8)
		if v >= limit {
			return nil, fmt.Errorf("packet: value %d does not fit in field %q (%d bytes, max %d)", v, f.Name, f.Size, limit-1)
		}
	}
	return packBytes(v, f.Size, f.EffectiveEndianness()), nil
}

// packBytes lays out the low n*8 bits of v into n wire bytes per end. Shared
// by field encoding and CRC packing (the CRC is not a Field, so it can't go
// through EncodeUint, but the byte layout rule is identical).
func packBytes(v uint64, n int, end Endianness) []byte {
	out := make([]byte, n)
	switch end {
	case LittleEndian:
		for i := 0; i < n; i++ {
			out[i] = byte(v >> uint(8*i))
		}
	default: // BigEndian
		for i := 0; i < n; i++ {
			out[n-1-i] = byte(v >> uint(8*i))
		}
	}
	return out
}

// unpackBytes is packBytes's inverse. len(raw) must be <= 8.
func unpackBytes(raw []byte, end Endianness) uint64 {
	var v uint64
	switch end {
	case LittleEndian:
		for i := len(raw) - 1; i >= 0; i-- {
			v = v<<8 | uint64(raw[i])
		}
	default:
		for i := 0; i < len(raw); i++ {
			v = v<<8 | uint64(raw[i])
		}
	}
	return v
}

// DecodeUint reads raw (must be len == field size, callers slice via
// Layout) as an unsigned integer per f's endianness. Fields wider than 8
// bytes cannot be represented as a uint64; ok is false in that case (Raw
// bytes remain the source of truth regardless).
func DecodeUint(f Field, raw []byte) (v uint64, ok bool) {
	if len(raw) > 8 {
		return 0, false
	}
	return unpackBytes(raw, f.EffectiveEndianness()), true
}

// DecodeInt reinterprets DecodeUint's result as two's-complement signed,
// sign-extended from f.Size*8 bits.
func DecodeInt(f Field, raw []byte) (v int64, ok bool) {
	u, ok := DecodeUint(f, raw)
	if !ok {
		return 0, false
	}
	bits := uint(len(raw) * 8)
	if bits == 64 {
		return int64(u), true
	}
	signBit := uint64(1) << (bits - 1)
	if u&signBit != 0 {
		u -= signBit << 1
	}
	return int64(u), true
}

// EncodeASCII returns raw bytes for a text value, right-padded with 0x00 if
// shorter than f.Size, and erroring if it overflows the field.
func EncodeASCII(f Field, s string) ([]byte, error) {
	if len(s) > f.Size {
		return nil, fmt.Errorf("packet: text %q (%d bytes) does not fit in field %q (%d bytes)", s, len(s), f.Name, f.Size)
	}
	out := make([]byte, f.Size)
	copy(out, s)
	return out, nil
}
