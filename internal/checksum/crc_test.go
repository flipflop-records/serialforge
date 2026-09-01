package checksum

import (
	"hash/crc32"
	"hash/crc64"
	"testing"
)

// TestPresetCheckVectors validates every catalogued preset against its
// published check value for the standard ASCII "123456789" self-check
// input, per the CRC RevEng catalogue convention.
func TestPresetCheckVectors(t *testing.T) {
	for _, p := range Presets {
		p := p
		t.Run(p.Name, func(t *testing.T) {
			c, err := New(p.Params)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got := c.Compute(CheckVector)
			if got != p.Check {
				t.Errorf("Compute(%q) = 0x%X, want 0x%X", CheckVector, got, p.Check)
			}
		})
	}
}

// TestCrossValidateStdlib checks the bitwise engine against Go's own
// hash/crc32 and hash/crc64 for known-good, independently implemented
// tables, over several inputs including edge cases (empty, single byte).
// This anchors correctness of the general algorithm to the standard
// library rather than to transcribed constants alone.
func TestCrossValidateStdlib(t *testing.T) {
	inputs := [][]byte{
		{},
		{0x00},
		{0xFF},
		[]byte("123456789"),
		[]byte("The quick brown fox jumps over the lazy dog"),
		{0xAA, 0x55, 0x00, 0xFF, 0x01, 0x02, 0x03, 0xFE, 0xFD, 0x10, 0x20, 0x30},
	}

	ieee, _ := New(mustLookup(t, "CRC-32/ISO-HDLC").Params)
	castagnoli, _ := New(mustLookup(t, "CRC-32C").Params)
	xz, _ := New(mustLookup(t, "CRC-64/XZ").Params)
	iso, _ := New(mustLookup(t, "CRC-64/ISO").Params)

	ieeeTab := crc32.IEEETable
	castTab := crc32.MakeTable(crc32.Castagnoli)
	xzTab := crc64.MakeTable(crc64.ECMA) // ECMA-182 poly matches CRC-64/XZ
	isoTab := crc64.MakeTable(crc64.ISO)

	for _, in := range inputs {
		if got, want := uint64(ieee.Compute(in)), uint64(crc32.Checksum(in, ieeeTab)); got != want {
			t.Errorf("CRC-32/ISO-HDLC(%v) = 0x%X, stdlib IEEE = 0x%X", in, got, want)
		}
		if got, want := uint64(castagnoli.Compute(in)), uint64(crc32.Checksum(in, castTab)); got != want {
			t.Errorf("CRC-32C(%v) = 0x%X, stdlib Castagnoli = 0x%X", in, got, want)
		}
		if got, want := xz.Compute(in), crc64.Checksum(in, xzTab); got != want {
			t.Errorf("CRC-64/XZ(%v) = 0x%X, stdlib ECMA = 0x%X", in, got, want)
		}
		if got, want := iso.Compute(in), crc64.Checksum(in, isoTab); got != want {
			t.Errorf("CRC-64/ISO(%v) = 0x%X, stdlib ISO = 0x%X", in, got, want)
		}
	}
}

func mustLookup(t *testing.T, name string) Preset {
	t.Helper()
	p, ok := Lookup(name)
	if !ok {
		t.Fatalf("preset %q not found", name)
	}
	return p
}

func TestCustomCRC(t *testing.T) {
	// A hand-built duplicate of CRC-8/MAXIM-DOW expressed as "custom"
	// parameters, as an engineer would enter them from a datasheet.
	custom := Params{Width: 8, Poly: 0x31, Init: 0x00, RefIn: true, RefOut: true, XorOut: 0x00}
	c, err := New(custom)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Compute(CheckVector); got != 0xA1 {
		t.Errorf("custom CRC-8/MAXIM-DOW = 0x%X, want 0xA1", got)
	}
}

func TestValidateRejectsOutOfRangeParams(t *testing.T) {
	cases := []Params{
		{Width: 0, Poly: 0x07},
		{Width: 65, Poly: 0x07},
		{Width: 8, Poly: 0x1FF}, // 9 bits in an 8-bit width
		{Width: 8, Poly: 0x07, Init: 0x1FF},
		{Width: 8, Poly: 0x07, XorOut: 0x1FF},
	}
	for _, p := range cases {
		if err := p.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want error", p)
		}
	}
}

func TestVerifyDetectsMismatch(t *testing.T) {
	c := MustNew(mustLookup(t, "CRC-8").Params)
	good := c.Compute(CheckVector)
	if !c.Verify(CheckVector, good) {
		t.Error("Verify should accept the correct CRC")
	}
	if c.Verify(CheckVector, good^0xFF) {
		t.Error("Verify should reject a corrupted CRC")
	}
}

func TestDefinitionResolution(t *testing.T) {
	none := Definition{Mode: ModeNone}
	crc, err := none.CRC()
	if err != nil || crc != nil {
		t.Fatalf("ModeNone CRC() = %v, %v; want nil, nil", crc, err)
	}

	preset := Definition{Mode: ModePreset, Preset: "CRC-16/MODBUS"}
	crc, err = preset.CRC()
	if err != nil {
		t.Fatalf("ModePreset CRC(): %v", err)
	}
	if got := crc.Compute(CheckVector); got != 0x4B37 {
		t.Errorf("preset CRC-16/MODBUS = 0x%X, want 0x4B37", got)
	}
	if w := preset.Width(); w != 16 {
		t.Errorf("Width() = %d, want 16", w)
	}

	bad := Definition{Mode: ModePreset, Preset: "does-not-exist"}
	if _, err := bad.CRC(); err == nil {
		t.Error("unknown preset should error")
	}
}
