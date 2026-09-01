package checksum

import "fmt"

// Lookup returns the built-in preset with the given name (case-sensitive,
// matching either Name or one of Aliases), or false if none matches.
func Lookup(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
		for _, a := range p.Aliases {
			if a == name {
				return p, true
			}
		}
	}
	return Preset{}, false
}

// Names returns every preset's canonical name, in catalog order — the list
// a picker UI iterates over.
func Names() []string {
	names := make([]string, len(Presets))
	for i, p := range Presets {
		names[i] = p.Name
	}
	return names
}

// Mode selects whether a Definition resolves to a catalog Preset or a
// user-authored set of Params.
type Mode string

const (
	ModeNone   Mode = "none"   // checksum disabled
	ModePreset Mode = "preset" // Definition.Preset names an entry in Presets
	ModeCustom Mode = "custom" // Definition.Custom carries the parameters directly
)

// Coverage describes which packet bytes feed the CRC. The default (and only
// mode wired into the packet serializer/decoder in v0.1) is CoverAllBeforeCRC:
// every byte preceding the CRC field. CoverRange exists in the model now so
// protocol profiles have a stable, forward-compatible place to record a
// narrower span once the packet engine grows selective-coverage support —
// see the "planned" note in ARCHITECTURE.md's "Packet schema model".
type Coverage struct {
	Mode  CoverageMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	Start int          `yaml:"start,omitempty" json:"start,omitempty"` // byte offset, CoverRange only
	End   int          `yaml:"end,omitempty" json:"end,omitempty"`     // exclusive byte offset, CoverRange only
}

type CoverageMode string

const (
	CoverAllBeforeCRC CoverageMode = "all_before_crc"
	CoverRange        CoverageMode = "range"
)

// Definition is the serializable, protocol-profile-facing description of a
// packet's checksum: which algorithm, and (today) that it covers every byte
// ahead of the CRC field. It is the type protocol.Profile embeds; CRC() only
// resolves it into a *CRC engine.
type Definition struct {
	Mode     Mode     `yaml:"mode" json:"mode"`
	Preset   string   `yaml:"preset,omitempty" json:"preset,omitempty"`
	Custom   Params   `yaml:"custom,omitempty" json:"custom,omitempty"`
	Coverage Coverage `yaml:"coverage" json:"coverage"`
	// Endianness controls how the CRC's numeric result is packed into the
	// packet's trailing bytes on the wire — independent of RefIn/RefOut,
	// which affect the algorithm's internal bit order, not byte packing.
	// Defaults to big-endian (most significant byte first) when empty.
	Endianness Endianness `yaml:"endianness,omitempty" json:"endianness,omitempty"`
}

// Endianness selects multi-byte wire packing order. Shared by field values
// and CRC packing so both use one vocabulary.
type Endianness string

const (
	BigEndian    Endianness = "big"
	LittleEndian Endianness = "little"
)

// EffectiveEndianness returns Endianness, defaulting to BigEndian.
func (d Definition) EffectiveEndianness() Endianness {
	if d.Endianness == "" {
		return BigEndian
	}
	return d.Endianness
}

// CRC resolves the definition into a ready CRC engine. Returns (nil, nil)
// for ModeNone — the caller's job is to treat a nil engine as "no checksum".
func (d Definition) CRC() (*CRC, error) {
	switch d.Mode {
	case "", ModeNone:
		return nil, nil
	case ModePreset:
		p, ok := Lookup(d.Preset)
		if !ok {
			return nil, fmt.Errorf("checksum: unknown preset %q", d.Preset)
		}
		return New(p.Params)
	case ModeCustom:
		return New(d.Custom)
	default:
		return nil, fmt.Errorf("checksum: unknown mode %q", d.Mode)
	}
}

// AlgorithmLabels returns display-name candidates for the checksum
// algorithm, most descriptive first: the preset's canonical Name, then its
// catalogued Aliases — which double as ready-made shorter names (e.g.
// "CRC-8/MAXIM" for "CRC-8/MAXIM-DOW") rather than an arbitrary
// abbreviation a renderer would otherwise have to invent. This is the one
// place CRC algorithm naming lives; a narrow packet-diagram cell or a
// field-list column picks whichever candidate fits instead of duplicating
// naming logic of its own (see ARCHITECTURE.md's CRC presentation invariants).
// ModeCustom has no catalogued name, so it returns a synthetic
// "CUSTOM CRC-N" / "CRC-N" / "CRC" ladder instead. ModeNone or an
// unresolvable preset name returns nil.
func (d Definition) AlgorithmLabels() []string {
	switch d.Mode {
	case ModePreset:
		if d.Preset == "" {
			return nil
		}
		p, ok := Lookup(d.Preset)
		if !ok {
			return []string{d.Preset}
		}
		labels := make([]string, 0, len(p.Aliases)+1)
		labels = append(labels, p.Name)
		labels = append(labels, p.Aliases...)
		return labels
	case ModeCustom:
		w := d.Custom.Width
		return []string{fmt.Sprintf("CUSTOM CRC-%d", w), fmt.Sprintf("CRC-%d", w), "CRC"}
	default:
		return nil
	}
}

// AlgorithmName is AlgorithmLabels's first (most descriptive) candidate, or
// the generic "CRC" when nothing is resolvable — the label a full-width
// display always has room for.
func (d Definition) AlgorithmName() string {
	if labels := d.AlgorithmLabels(); len(labels) > 0 {
		return labels[0]
	}
	return "CRC"
}

// Width returns the CRC width in bits, or 0 if disabled/unresolvable.
func (d Definition) Width() int {
	switch d.Mode {
	case ModePreset:
		if p, ok := Lookup(d.Preset); ok {
			return p.Params.Width
		}
	case ModeCustom:
		return d.Custom.Width
	}
	return 0
}
