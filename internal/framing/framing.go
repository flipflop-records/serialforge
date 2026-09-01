// Package framing turns a raw byte stream into discrete frames. It knows
// nothing about packet schemas or CRCs — internal/session pushes bytes in
// and hands complete frames up to whatever's listening (the monitor, the
// packet decoder via internal/packet.Decode, a batch expect step). See
// product spec §20 for the four framing modes this implements, and its note
// that COBS/SLIP/HDLC/length-field framing are a deliberately deferred
// extension, not built now.
package framing

import (
	"bytes"
	"errors"
	"fmt"
)

// Kind selects a framing strategy.
type Kind string

const (
	KindRaw       Kind = "raw"       // every Push is itself a frame; no buffering
	KindLine      Kind = "line"      // split on '\n', optionally trimming a trailing '\r'
	KindFixed     Kind = "fixed"     // emit exactly Size-byte frames
	KindDelimiter Kind = "delimiter" // split on an arbitrary byte sequence
)

// Options configures a Framer. Only the fields relevant to Kind are used.
type Options struct {
	Delimiter []byte // KindDelimiter
	TrimCR    bool   // KindLine: drop a trailing '\r' from each line (CRLF sources)
	Size      int    // KindFixed: bytes per frame, must be >= 1
}

// Framer accumulates pushed bytes and yields complete frames. It is not
// safe for concurrent use — internal/session owns one Framer per RX loop
// and calls it only from that goroutine.
type Framer interface {
	// Push appends newly read bytes to the internal buffer.
	Push(data []byte)
	// Next pops one complete frame if available. Call it in a loop after
	// each Push until ok is false.
	Next() (frame []byte, ok bool)
	// Reset discards any partial, unframed bytes (batch's clear_rx step).
	Reset()
}

// New builds a Framer for kind. Returns an error for KindFixed with Size<1
// or KindDelimiter with an empty Delimiter — those are configuration
// mistakes the caller should surface immediately, not silently misbehave
// on the first Push.
func New(kind Kind, opts Options) (Framer, error) {
	switch kind {
	case "", KindRaw:
		return &rawFramer{}, nil
	case KindLine:
		return &delimiterFramer{delim: []byte("\n"), trimCR: opts.TrimCR}, nil
	case KindDelimiter:
		if len(opts.Delimiter) == 0 {
			return nil, errNoDelimiter
		}
		return &delimiterFramer{delim: opts.Delimiter}, nil
	case KindFixed:
		if opts.Size < 1 {
			return nil, errBadFixedSize
		}
		return &fixedFramer{size: opts.Size}, nil
	default:
		return nil, fmt.Errorf("framing: unknown kind %q", kind)
	}
}

var (
	errNoDelimiter  = errors.New("framing: KindDelimiter requires a non-empty Delimiter")
	errBadFixedSize = errors.New("framing: KindFixed requires Size >= 1")
)

type rawFramer struct {
	buf [][]byte
}

func (f *rawFramer) Push(data []byte) {
	if len(data) == 0 {
		return
	}
	cp := append([]byte(nil), data...)
	f.buf = append(f.buf, cp)
}

func (f *rawFramer) Next() ([]byte, bool) {
	if len(f.buf) == 0 {
		return nil, false
	}
	frame := f.buf[0]
	f.buf = f.buf[1:]
	return frame, true
}

func (f *rawFramer) Reset() { f.buf = nil }

type fixedFramer struct {
	size int
	buf  []byte
}

func (f *fixedFramer) Push(data []byte) { f.buf = append(f.buf, data...) }

func (f *fixedFramer) Next() ([]byte, bool) {
	if len(f.buf) < f.size {
		return nil, false
	}
	frame := append([]byte(nil), f.buf[:f.size]...)
	f.buf = f.buf[f.size:]
	return frame, true
}

func (f *fixedFramer) Reset() { f.buf = nil }

type delimiterFramer struct {
	delim  []byte
	trimCR bool
	buf    []byte
}

func (f *delimiterFramer) Push(data []byte) { f.buf = append(f.buf, data...) }

func (f *delimiterFramer) Next() ([]byte, bool) {
	i := bytes.Index(f.buf, f.delim)
	if i < 0 {
		return nil, false
	}
	frame := f.buf[:i]
	f.buf = f.buf[i+len(f.delim):]
	if f.trimCR && len(frame) > 0 && frame[len(frame)-1] == '\r' {
		frame = frame[:len(frame)-1]
	}
	return append([]byte(nil), frame...), true
}

func (f *delimiterFramer) Reset() { f.buf = nil }
