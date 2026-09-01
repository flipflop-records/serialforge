// Package serial is the cross-platform serial transport boundary: a small
// Port interface, a real implementation over go.bug.st/serial, an
// in-memory FakePort for hardware-free testing, and port enumeration.
// Nothing above this package (session, batch, tui, cmd) may reach for an OS
// serial handle directly — see the "TUI code never touches an OS serial
// handle" invariant in ARCHITECTURE.md.
package serial

import "fmt"

// Parity mirrors the standard UART parity modes.
type Parity string

const (
	ParityNone  Parity = "none"
	ParityOdd   Parity = "odd"
	ParityEven  Parity = "even"
	ParityMark  Parity = "mark"
	ParitySpace Parity = "space"
)

// StopBits mirrors the standard UART stop-bit counts.
type StopBits string

const (
	StopBits1   StopBits = "1"
	StopBits1_5 StopBits = "1.5"
	StopBits2   StopBits = "2"
)

// FlowControl selects hardware/software flow control, or none.
type FlowControl string

const (
	FlowNone    FlowControl = "none"
	FlowRTSCTS  FlowControl = "rts_cts"
	FlowXonXoff FlowControl = "xon_xoff"
)

// BaudPresets lists the common baud rates the UI offers as quick picks.
// Arbitrary values remain valid — Config.Baud is a plain int, not an enum.
var BaudPresets = []int{9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600}

// Config describes how to open a serial port. Zero value is not valid on
// its own — DefaultConfig gives sane starting values (8N1 at 115200).
type Config struct {
	Baud        int
	DataBits    int // 5..8
	Parity      Parity
	StopBits    StopBits
	FlowControl FlowControl
	// ReadTimeout bounds a single Read call on the real transport (0 means
	// block until data, error, or Close — see go.bug.st/serial's
	// SetReadTimeout semantics, which this maps to directly). Framers and
	// the session's expect/timeout logic should generally prefer their own
	// context/timer-based deadlines over relying on this; it exists mainly
	// so Close() reliably unblocks a pending Read on every platform.
	ReadTimeout int64 // milliseconds; <0 means block forever
}

// DefaultConfig returns 115200 8N1, no flow control, 200ms read timeout.
func DefaultConfig() Config {
	return Config{
		Baud:        115200,
		DataBits:    8,
		Parity:      ParityNone,
		StopBits:    StopBits1,
		FlowControl: FlowNone,
		ReadTimeout: 200,
	}
}

// FrameString renders the byte-framing portion of cfg the way engineers
// write it on a datasheet or a terminal-emulator status line — "8N1",
// "7E2" — data bits + a one-letter parity code + stop bits. Baud is
// reported separately (it isn't part of "the frame").
func (c Config) FrameString() string {
	return fmt.Sprintf("%d%s%s", c.DataBits, parityLetter(c.Parity), c.StopBits)
}

func parityLetter(p Parity) string {
	switch p {
	case ParityOdd:
		return "O"
	case ParityEven:
		return "E"
	case ParityMark:
		return "M"
	case ParitySpace:
		return "S"
	default:
		return "N"
	}
}

// Validate reports whether the config can be opened.
func (c Config) Validate() error {
	if c.Baud <= 0 {
		return fmt.Errorf("serial: baud rate must be positive, got %d", c.Baud)
	}
	if c.DataBits < 5 || c.DataBits > 8 {
		return fmt.Errorf("serial: data bits must be 5..8, got %d", c.DataBits)
	}
	switch c.Parity {
	case ParityNone, ParityOdd, ParityEven, ParityMark, ParitySpace:
	default:
		return fmt.Errorf("serial: unknown parity %q", c.Parity)
	}
	switch c.StopBits {
	case StopBits1, StopBits1_5, StopBits2:
	default:
		return fmt.Errorf("serial: unknown stop bits %q", c.StopBits)
	}
	switch c.FlowControl {
	case FlowNone, FlowRTSCTS, FlowXonXoff:
	default:
		return fmt.Errorf("serial: unknown flow control %q", c.FlowControl)
	}
	return nil
}
