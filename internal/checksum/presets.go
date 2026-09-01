package checksum

// Preset is a named, catalogued CRC definition. Check is the algorithm's
// value over the ASCII string "123456789" (9 bytes) — the standard
// verification vector used by the CRC RevEng catalogue
// (reveng.sourceforge.io/crc-catalogue) and reproduced by virtually every
// CRC reference implementation. presets_test.go asserts every entry below
// against its Check value, and cross-checks the 32/64-bit entries against
// Go's own stdlib hash/crc32 and hash/crc64 implementations so correctness
// does not rest on transcription alone.
type Preset struct {
	Name    string
	Aliases []string
	Params  Params
	Check   uint64
}

// CheckVector is the standard CRC catalogue self-check input.
var CheckVector = []byte("123456789")

// Presets lists the built-in, catalogue-verified CRC algorithms, ordered by
// width then name. Field TX/RX and the protocol designer's CRC picker both
// read from this table (see internal/checksum/registry.go).
var Presets = []Preset{
	{
		Name:   "CRC-8",
		Params: Params{Width: 8, Poly: 0x07, Init: 0x00, RefIn: false, RefOut: false, XorOut: 0x00},
		Check:  0xF4,
	},
	{
		Name:    "CRC-8/MAXIM-DOW",
		Aliases: []string{"CRC-8/MAXIM", "DOW-CRC", "1-WIRE"},
		Params:  Params{Width: 8, Poly: 0x31, Init: 0x00, RefIn: true, RefOut: true, XorOut: 0x00},
		Check:   0xA1,
	},
	{
		Name:   "CRC-8/SAE-J1850",
		Params: Params{Width: 8, Poly: 0x1D, Init: 0xFF, RefIn: false, RefOut: false, XorOut: 0xFF},
		Check:  0x4B,
	},
	{
		Name:   "CRC-8/NRSC-5",
		Params: Params{Width: 8, Poly: 0x31, Init: 0xFF, RefIn: false, RefOut: false, XorOut: 0x00},
		Check:  0xF7,
	},
	{
		Name:    "CRC-16/ARC",
		Aliases: []string{"CRC-16", "CRC-16/LHA", "ARC"},
		Params:  Params{Width: 16, Poly: 0x8005, Init: 0x0000, RefIn: true, RefOut: true, XorOut: 0x0000},
		Check:   0xBB3D,
	},
	{
		Name:   "CRC-16/MODBUS",
		Params: Params{Width: 16, Poly: 0x8005, Init: 0xFFFF, RefIn: true, RefOut: true, XorOut: 0x0000},
		Check:  0x4B37,
	},
	{
		Name:    "CRC-16/CCITT-FALSE",
		Aliases: []string{"CRC-16/IBM-3740"},
		Params:  Params{Width: 16, Poly: 0x1021, Init: 0xFFFF, RefIn: false, RefOut: false, XorOut: 0x0000},
		Check:   0x29B1,
	},
	{
		Name:    "CRC-16/XMODEM",
		Aliases: []string{"CRC-16/ACORN", "CRC-16/ZMODEM"},
		Params:  Params{Width: 16, Poly: 0x1021, Init: 0x0000, RefIn: false, RefOut: false, XorOut: 0x0000},
		Check:   0x31C3,
	},
	{
		Name:    "CRC-16/KERMIT",
		Aliases: []string{"CRC-16/CCITT", "CRC-16/CCITT-TRUE"},
		Params:  Params{Width: 16, Poly: 0x1021, Init: 0x0000, RefIn: true, RefOut: true, XorOut: 0x0000},
		Check:   0x2189,
	},
	{
		Name:   "CRC-16/DNP",
		Params: Params{Width: 16, Poly: 0x3D65, Init: 0x0000, RefIn: true, RefOut: true, XorOut: 0xFFFF},
		Check:  0xEA82,
	},
	{
		Name:    "CRC-32/ISO-HDLC",
		Aliases: []string{"CRC-32", "CRC-32/ADCCP", "PKZIP"},
		Params:  Params{Width: 32, Poly: 0x04C11DB7, Init: 0xFFFFFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFFFFFF},
		Check:   0xCBF43926,
	},
	{
		Name:    "CRC-32C",
		Aliases: []string{"CRC-32/ISCSI", "CRC-32/CASTAGNOLI", "CRC-32/BASE91-C"},
		Params:  Params{Width: 32, Poly: 0x1EDC6F41, Init: 0xFFFFFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFFFFFF},
		Check:   0xE3069283,
	},
	{
		Name:   "CRC-32/BZIP2",
		Params: Params{Width: 32, Poly: 0x04C11DB7, Init: 0xFFFFFFFF, RefIn: false, RefOut: false, XorOut: 0xFFFFFFFF},
		Check:  0xFC891918,
	},
	{
		Name:   "CRC-32/MPEG-2",
		Params: Params{Width: 32, Poly: 0x04C11DB7, Init: 0xFFFFFFFF, RefIn: false, RefOut: false, XorOut: 0x00000000},
		Check:  0x0376E6E7,
	},
	{
		Name:    "CRC-64/XZ",
		Aliases: []string{"CRC-64/GO-ECMA"},
		Params:  Params{Width: 64, Poly: 0x42F0E1EBA9EA3693, Init: 0xFFFFFFFFFFFFFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFFFFFFFFFFFFFF},
		Check:   0x995DC9BBDF1939FA,
	},
	{
		Name:   "CRC-64/ISO",
		Params: Params{Width: 64, Poly: 0x000000000000001B, Init: 0xFFFFFFFFFFFFFFFF, RefIn: true, RefOut: true, XorOut: 0xFFFFFFFFFFFFFFFF},
		Check:  0xB90956C775A41001,
	},
}
