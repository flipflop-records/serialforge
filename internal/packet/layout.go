package packet

// SpanKind classifies one segment of a packet's Layout.
type SpanKind string

const (
	SpanField       SpanKind = "field"
	SpanCRC         SpanKind = "crc"
	SpanUnallocated SpanKind = "unallocated"
)

// Span is one contiguous, labeled byte range of a packet. Layout() is the
// single reusable representation the register-style diagram, the TX
// builder, the RX inspector, and the schema editor all render from — see
// the package doc comment for why there must be exactly one of these.
type Span struct {
	Kind       SpanKind
	Name       string
	Offset     int
	Size       int
	FieldIndex int // index into Schema.Fields; -1 for SpanCRC/SpanUnallocated
}

// End returns the exclusive end offset of the span.
func (sp Span) End() int { return sp.Offset + sp.Size }

// Layout returns the packet's byte ranges in order: every field, then
// (if enabled) the CRC, then — only while the schema is still under
// construction in the editor — a trailing SpanUnallocated span covering
// whatever bytes remain. A schema that Validate()s clean never has an
// unallocated span, since Allocated() == TotalSize is exactly what
// Validate checks.
func (s Schema) Layout() []Span {
	spans := make([]Span, 0, len(s.Fields)+2)
	off := 0
	for i, f := range s.Fields {
		spans = append(spans, Span{Kind: SpanField, Name: f.Name, Offset: off, Size: f.Size, FieldIndex: i})
		off += f.Size
	}
	if crcOff, crcSize, ok := s.CRCOffset(); ok {
		spans = append(spans, Span{Kind: SpanCRC, Name: s.crcLabel(), Offset: crcOff, Size: crcSize, FieldIndex: -1})
		off = crcOff + crcSize
	}
	if remaining := s.TotalSize - off; remaining > 0 {
		spans = append(spans, Span{Kind: SpanUnallocated, Name: "UNALLOCATED", Offset: off, Size: remaining, FieldIndex: -1})
	}
	return spans
}

// crcLabel is the CRC span's display name — the checksum model's own
// AlgorithmName (e.g. "CRC-8/MAXIM-DOW"), not a locally invented one. A
// renderer with limited space (the register diagram's cell) picks a
// shorter checksum.Definition.AlgorithmLabels candidate itself rather than
// truncating this one; see internal/tui/diagram.go.
func (s Schema) crcLabel() string {
	return s.Checksum.AlgorithmName()
}
