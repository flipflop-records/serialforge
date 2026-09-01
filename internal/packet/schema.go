// Package packet implements the packet-schema model that is the product's
// central abstraction: one description of a binary packet's layout, shared
// verbatim by the protocol designer's live diagram, the TX builder, the RX
// decoder, the packet inspector, and the batch engine. Nothing in this
// package touches a serial port, a terminal, or a file — see
// internal/protocol for schema persistence and internal/tui for rendering.
package packet

import (
	"fmt"

	"github.com/vtemnyakov/serialforge/internal/checksum"
)

// Endianness is re-exported from checksum so callers only import one
// vocabulary for byte order across fields and the CRC.
type Endianness = checksum.Endianness

const (
	BigEndian    = checksum.BigEndian
	LittleEndian = checksum.LittleEndian
)

// Format selects how a field's raw bytes are interpreted for display and
// for value entry. The raw bytes themselves are always preserved
// regardless of Format — Format only changes how they are shown/parsed.
type Format string

const (
	FormatHex   Format = "hex"   // e.g. "AA 55"
	FormatUint  Format = "uint"  // unsigned integer, per Endianness
	FormatInt   Format = "int"   // two's-complement signed integer, per Endianness
	FormatASCII Format = "ascii" // raw bytes shown/entered as text
	FormatRaw   Format = "raw"   // byte-for-byte, no numeric interpretation
	FormatEnum  Format = "enum"  // unsigned integer looked up in Field.Enum
)

// Field describes one named region of a packet. Size is authoritative and
// always in bytes — sub-byte (bit-level) fields are a documented future
// extension (see ARCHITECTURE.md "Packet schema model"), not implemented in v0.1, so
// every Field occupies a whole number of bytes at a byte-aligned offset.
type Field struct {
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Size        int               `yaml:"size" json:"size"` // bytes, >= 1
	Endianness  Endianness        `yaml:"endianness,omitempty" json:"endianness,omitempty"`
	Format      Format            `yaml:"format,omitempty" json:"format,omitempty"`
	Enum        map[uint64]string `yaml:"enum,omitempty" json:"enum,omitempty"` // FormatEnum only
}

// EffectiveEndianness returns Endianness, defaulting to BigEndian.
func (f Field) EffectiveEndianness() Endianness {
	if f.Endianness == "" {
		return BigEndian
	}
	return f.Endianness
}

// EffectiveFormat returns Format, defaulting to FormatHex.
func (f Field) EffectiveFormat() Format {
	if f.Format == "" {
		return FormatHex
	}
	return f.Format
}

// Schema is the one reusable description of a packet's byte layout: total
// size, an ordered list of fields, and an optional checksum. It is the
// single source of truth every subsystem (visualizer, TX builder, RX
// decoder, batch engine) reads — see the package doc comment.
type Schema struct {
	Name        string              `yaml:"name" json:"name"`
	Description string              `yaml:"description,omitempty" json:"description,omitempty"`
	TotalSize   int                 `yaml:"total_size" json:"total_size"` // bytes
	Fields      []Field             `yaml:"fields" json:"fields"`
	Checksum    checksum.Definition `yaml:"checksum" json:"checksum"`
}

// FieldsSize returns the sum of every field's Size, excluding the CRC.
func (s Schema) FieldsSize() int {
	n := 0
	for _, f := range s.Fields {
		n += f.Size
	}
	return n
}

// CRCSize returns the CRC's byte width (0 if disabled).
func (s Schema) CRCSize() int {
	if s.Checksum.Mode == "" || s.Checksum.Mode == checksum.ModeNone {
		return 0
	}
	return (s.Checksum.Width() + 7) / 8
}

// Allocated returns the number of packet bytes currently spoken for by
// fields plus the CRC.
func (s Schema) Allocated() int {
	return s.FieldsSize() + s.CRCSize()
}

// Remaining returns TotalSize - Allocated (can be negative if over-allocated;
// Validate is what rejects that, Remaining just reports the arithmetic so
// the editor can show "Remaining: -3 B" while the user is mid-edit).
func (s Schema) Remaining() int {
	return s.TotalSize - s.Allocated()
}

// FieldOffset returns the byte offset of the field at index i.
func (s Schema) FieldOffset(i int) int {
	off := 0
	for j := 0; j < i && j < len(s.Fields); j++ {
		off += s.Fields[j].Size
	}
	return off
}

// CRCOffset returns the CRC's byte offset and size, and whether a checksum
// is enabled at all. The CRC is always packet-final and byte-aligned — see
// ARCHITECTURE.md's "CRC is packet-final" invariant and "CRC engine"
// section.
func (s Schema) CRCOffset() (offset, size int, ok bool) {
	size = s.CRCSize()
	if size == 0 {
		return 0, 0, false
	}
	return s.FieldsSize(), size, true
}

// FieldByName returns the field with the given name and its index, or
// (Field{}, -1, false) if not found.
func (s Schema) FieldByName(name string) (Field, int, bool) {
	for i, f := range s.Fields {
		if f.Name == name {
			return f, i, true
		}
	}
	return Field{}, -1, false
}

// Validate reports every reason this schema is not yet a valid, serializable
// packet layout: the "impossible layout" prevention called out as a hard
// requirement in the product spec (fields + CRC must exactly fill
// TotalSize; no duplicate/empty names; no zero-size fields; CRC width must
// be byte-aligned and resolvable).
func (s Schema) Validate() error {
	if s.TotalSize <= 0 {
		return fmt.Errorf("packet: total size must be > 0, got %d", s.TotalSize)
	}
	seen := make(map[string]bool, len(s.Fields))
	for i, f := range s.Fields {
		if f.Name == "" {
			return fmt.Errorf("packet: field %d has no name", i)
		}
		if seen[f.Name] {
			return fmt.Errorf("packet: duplicate field name %q", f.Name)
		}
		seen[f.Name] = true
		if f.Size < 1 {
			return fmt.Errorf("packet: field %q has invalid size %d (must be >= 1 byte)", f.Name, f.Size)
		}
		if f.EffectiveFormat() == FormatEnum && len(f.Enum) == 0 {
			return fmt.Errorf("packet: field %q uses FormatEnum but defines no enum values", f.Name)
		}
	}
	if s.Checksum.Mode != "" && s.Checksum.Mode != checksum.ModeNone {
		crc, err := s.Checksum.CRC()
		if err != nil {
			return fmt.Errorf("packet: checksum: %w", err)
		}
		if !crc.Params().PackedByteAligned() {
			return fmt.Errorf("packet: checksum width %d bits is not byte-aligned; v0.1 requires a whole number of CRC bytes", crc.Params().Width)
		}
	}
	if got, want := s.Allocated(), s.TotalSize; got != want {
		if got < want {
			return fmt.Errorf("packet: fields + checksum allocate %d of %d bytes (%d bytes unallocated)", got, want, want-got)
		}
		return fmt.Errorf("packet: fields + checksum allocate %d bytes, exceeding the configured total size of %d bytes by %d", got, want, got-want)
	}
	return nil
}

// IsValid is a convenience wrapper over Validate.
func (s Schema) IsValid() bool {
	return s.Validate() == nil
}

// Clone returns a deep-enough copy of s safe to mutate (fields slice and
// each field's Enum map are copied) — used by the editor to stage changes
// that can be discarded, and by "duplicate protocol" operations.
func (s Schema) Clone() Schema {
	out := s
	out.Fields = make([]Field, len(s.Fields))
	for i, f := range s.Fields {
		nf := f
		if f.Enum != nil {
			nf.Enum = make(map[uint64]string, len(f.Enum))
			for k, v := range f.Enum {
				nf.Enum[k] = v
			}
		}
		out.Fields[i] = nf
	}
	return out
}
