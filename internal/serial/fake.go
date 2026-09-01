package serial

import (
	"io"
	"sync"
)

// FakePort is an in-memory Port for tests and simulated batch runs — no
// hardware required. NewFakePort returns the Port half the code under test opens, and
// the FakeDevice half a test uses to play the role of the physical device:
// FakeDevice.Write feeds bytes the FakePort will Read; FakePort.Write shows
// up on FakeDevice.Read.
//
// Both directions are backed by unbuffered io.Pipe, so Write blocks until a
// concurrent Read on the paired handle is ready to receive it — same as a
// stalled link with no OS driver buffer to absorb the write. Callers that
// exercise the TX direction in a test must have something already reading
// FakeDevice (or run the Write in a goroutine) before/while calling
// FakePort.Write, exactly as they would need a concurrent reader on the RX
// direction.
type FakePort struct {
	rx *io.PipeReader // device -> app
	tx *io.PipeWriter // app -> device

	mu     sync.Mutex
	closed bool
}

// FakeDevice is the test-side handle paired with a FakePort.
type FakeDevice struct {
	rx *io.PipeWriter // device -> app
	tx *io.PipeReader // app -> device
}

// NewFakePort creates a connected FakePort/FakeDevice pair.
func NewFakePort() (*FakePort, *FakeDevice) {
	rxReader, rxWriter := io.Pipe() // device -> app: FakeDevice.Send writes rxWriter, FakePort.Read reads rxReader
	txReader, txWriter := io.Pipe() // app -> device: FakePort.Write writes txWriter, FakeDevice.Recv reads txReader
	return &FakePort{rx: rxReader, tx: txWriter},
		&FakeDevice{rx: rxWriter, tx: txReader}
}

func (f *FakePort) Read(p []byte) (int, error)  { return f.rx.Read(p) }
func (f *FakePort) Write(p []byte) (int, error) { return f.tx.Write(p) }

// SetReadTimeout is a documented no-op: io.Pipe has no read-deadline
// concept, and callers needing a bounded wait (e.g. batch expect steps)
// should race Read against a context/timer instead — see internal/batch.
func (f *FakePort) SetReadTimeout(ms int64) error { return nil }

func (f *FakePort) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	// Closing our ends unblocks any pending Read/Write on both this Port
	// and the paired FakeDevice with io.ErrClosedPipe/EOF, mirroring a real
	// unplugged cable rather than hanging a test forever.
	_ = f.rx.Close()
	return f.tx.Close()
}

// Write makes data available to the FakePort's Read, as if the simulated
// device transmitted it. FakeDevice implements io.ReadWriteCloser so it
// composes with io.ReadFull/io.Copy/etc in tests.
func (d *FakeDevice) Write(data []byte) (int, error) { return d.rx.Write(data) }

// Read reads bytes the FakePort wrote (the "device" receiving what the
// application sent).
func (d *FakeDevice) Read(p []byte) (int, error) { return d.tx.Read(p) }

// Close shuts down the device side, which in turn unblocks the paired
// FakePort's pending Read/Write calls.
func (d *FakeDevice) Close() error {
	_ = d.rx.Close()
	return d.tx.Close()
}
