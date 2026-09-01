// Package checksum implements a parametric CRC engine (the "Rocksoft" CRC
// model: width, poly, init, refin, refout, xorout) capable of expressing
// every catalogued CRC-8/16/32/64 algorithm plus fully custom definitions.
//
// The engine is deliberately independent of the packet package: it knows
// nothing about fields, schemas, or the TUI. internal/packet integrates it
// by treating a CRC as one more field whose value is computed instead of
// user-supplied. See Params for the parameter model and Presets for the
// built-in catalog.
package checksum

import "fmt"

// Params fully describes a CRC algorithm using the parameter model
// popularised by Ross Williams' "A Painless Guide to CRC Error Detection
// Algorithms" and used by the catalogue at reveng.sourceforge.io/crc-catalogue.
//
// Poly, Init and XorOut are always given in normal (non-reflected, MSB-first)
// form regardless of RefIn/RefOut — that is the convention the catalogue and
// most datasheets use, and it is what lets Params be compared directly
// against a datasheet table.
type Params struct {
	// Width is the CRC width in bits, 1..64.
	Width int
	// Poly is the generator polynomial, normal form, low Width bits significant.
	Poly uint64
	// Init is the register's initial value, normal form.
	Init uint64
	// RefIn reflects (bit-reverses) each input byte before it enters the register.
	RefIn bool
	// RefOut reflects the final register value (before XorOut) if true.
	RefOut bool
	// XorOut is XORed with the final (possibly reflected) register value.
	XorOut uint64
}

// Validate checks that the parameters describe a representable CRC.
func (p Params) Validate() error {
	if p.Width < 8 || p.Width > 64 {
		return fmt.Errorf("checksum: width must be 8..64 bits, got %d (widths below 8 bits are not yet supported by the engine)", p.Width)
	}
	mask := widthMask(p.Width)
	if p.Poly&^mask != 0 {
		return fmt.Errorf("checksum: polynomial 0x%X has bits set above width %d", p.Poly, p.Width)
	}
	if p.Init&^mask != 0 {
		return fmt.Errorf("checksum: init 0x%X has bits set above width %d", p.Init, p.Width)
	}
	if p.XorOut&^mask != 0 {
		return fmt.Errorf("checksum: xorout 0x%X has bits set above width %d", p.XorOut, p.Width)
	}
	return nil
}

// ByteWidth reports how many whole bytes the CRC occupies once packed into
// a packet. v0.1 requires byte-aligned widths (see CRC WIDTH AND PACKING in
// the product spec) — a non-multiple-of-8 width is rejected by Validate
// callers that pack into a packet (PackedWidth), though the bit-level engine
// itself works for any width and can still be used standalone (e.g. for
// verifying a datasheet's odd-width example) via Compute/Verify.
func (p Params) ByteWidth() int {
	return (p.Width + 7) / 8
}

// PackedByteAligned reports whether Width is a whole number of bytes.
func (p Params) PackedByteAligned() bool {
	return p.Width%8 == 0
}

func widthMask(width int) uint64 {
	if width >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << uint(width)) - 1
}

// CRC is a ready-to-use CRC algorithm instance.
type CRC struct {
	params Params
	mask   uint64
	topBit uint64
}

// New builds a CRC engine from params. It returns an error if the
// parameters are not representable (see Params.Validate).
func New(p Params) (*CRC, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &CRC{
		params: p,
		mask:   widthMask(p.Width),
		topBit: uint64(1) << uint(p.Width-1),
	}, nil
}

// MustNew is New but panics on error; for package-level preset tables where
// the parameters are known-good at compile time.
func MustNew(p Params) *CRC {
	c, err := New(p)
	if err != nil {
		panic(err)
	}
	return c
}

// Params returns the parameters this engine was built from.
func (c *CRC) Params() Params { return c.params }

// Compute runs the CRC over data and returns the final value, masked to
// Width bits. This is the classic byte-serial bit-by-bit simulation of the
// CRC LFSR: each (optionally reflected) input byte is XORed into the top
// byte-position of the Width-bit register, then shifted through 8 times,
// conditionally XORing Poly on every bit whose vacated top bit was set.
// Packets in this application are small (bytes to low kilobytes), so a
// table-driven implementation buys nothing; the bit-serial form is also the
// easiest to verify against a datasheet bit-for-bit. Requires Width >= 8
// (enforced by New/Validate) — the byte-XOR alignment this algorithm relies
// on does not generalize below one byte.
func (c *CRC) Compute(data []byte) uint64 {
	reg := c.params.Init & c.mask
	shift := uint(c.params.Width - 8)
	for _, raw := range data {
		b := raw
		if c.params.RefIn {
			b = reverseByte(b)
		}
		reg ^= uint64(b) << shift
		for i := 0; i < 8; i++ {
			if reg&c.topBit != 0 {
				reg = ((reg << 1) ^ c.params.Poly) & c.mask
			} else {
				reg = (reg << 1) & c.mask
			}
		}
	}
	if c.params.RefOut {
		reg = reverseBits(reg, c.params.Width)
	}
	return (reg ^ c.params.XorOut) & c.mask
}

// Verify reports whether data's trailing/accompanying CRC equals want.
func (c *CRC) Verify(data []byte, want uint64) bool {
	return c.Compute(data) == want
}

func reverseByte(b byte) byte {
	b = (b&0xF0)>>4 | (b&0x0F)<<4
	b = (b&0xCC)>>2 | (b&0x33)<<2
	b = (b&0xAA)>>1 | (b&0x55)<<1
	return b
}

func reverseBits(x uint64, width int) uint64 {
	var r uint64
	for i := 0; i < width; i++ {
		r <<= 1
		r |= x & 1
		x >>= 1
	}
	return r
}
