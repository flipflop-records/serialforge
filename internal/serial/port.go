package serial

import (
	"fmt"
	"io"
	"time"

	gobugst "go.bug.st/serial"
)

// Port is the transport interface everything above this package programs
// against — a real UART/USB-serial device (realPort) or an in-memory
// FakePort in tests. Deliberately small: framing, timeouts-as-deadlines,
// and reconnect policy all live one layer up in internal/session.
type Port interface {
	io.ReadWriteCloser
	// SetReadTimeout changes the blocking-read deadline without closing and
	// reopening the port. ms < 0 blocks forever; ms == 0 returns
	// immediately with whatever is available.
	SetReadTimeout(ms int64) error
}

// Open opens the OS serial device at path with cfg. path is whatever the
// platform calls it — "/dev/cu.usbserial-1410", "/dev/ttyUSB0", "COM3" —
// internal/device is what resolves a friendly alias down to this string;
// this package never assumes a path format.
func Open(path string, cfg Config) (Port, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	mode := &gobugst.Mode{
		BaudRate: cfg.Baud,
		DataBits: cfg.DataBits,
		Parity:   toLibParity(cfg.Parity),
		StopBits: toLibStopBits(cfg.StopBits),
	}
	p, err := gobugst.Open(path, mode)
	if err != nil {
		return nil, fmt.Errorf("serial: open %s: %w", path, err)
	}
	if err := applyFlowControl(p, cfg.FlowControl); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.SetReadTimeout(msToDuration(cfg.ReadTimeout)); err != nil {
		p.Close()
		return nil, fmt.Errorf("serial: set read timeout: %w", err)
	}
	return &realPort{p: p}, nil
}

type realPort struct {
	p gobugst.Port
}

func (r *realPort) Read(b []byte) (int, error)  { return r.p.Read(b) }
func (r *realPort) Write(b []byte) (int, error) { return r.p.Write(b) }
func (r *realPort) Close() error                { return r.p.Close() }

func (r *realPort) SetReadTimeout(ms int64) error {
	return r.p.SetReadTimeout(msToDuration(ms))
}

func toLibParity(p Parity) gobugst.Parity {
	switch p {
	case ParityOdd:
		return gobugst.OddParity
	case ParityEven:
		return gobugst.EvenParity
	case ParityMark:
		return gobugst.MarkParity
	case ParitySpace:
		return gobugst.SpaceParity
	default:
		return gobugst.NoParity
	}
}

func toLibStopBits(s StopBits) gobugst.StopBits {
	switch s {
	case StopBits1_5:
		return gobugst.OnePointFiveStopBits
	case StopBits2:
		return gobugst.TwoStopBits
	default:
		return gobugst.OneStopBit
	}
}

// applyFlowControl sets RTS/CTS where the library exposes it directly.
// go.bug.st/serial does not model XON/XOFF software flow control as a Mode
// field; FlowXonXoff is accepted by Config.Validate as a documented value
// for protocol profiles/UI purposes but is not yet wired to a real
// implementation — see ARCHITECTURE.md "Known limitations".
func applyFlowControl(p gobugst.Port, fc FlowControl) error {
	switch fc {
	case FlowRTSCTS:
		return p.SetRTS(true)
	default:
		return nil
	}
}

// msToDuration converts Config.ReadTimeout's millisecond convention to the
// library's time.Duration, mapping any negative value to gobugst.NoTimeout
// (block forever) rather than an arbitrary negative Duration.
func msToDuration(ms int64) time.Duration {
	if ms < 0 {
		return gobugst.NoTimeout
	}
	return time.Duration(ms) * time.Millisecond
}
