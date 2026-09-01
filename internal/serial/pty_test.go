//go:build !windows

package serial

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPTYRoundTrip is the integration test for manual/virtual serial paths
// (macOS PTY / development workflow — see ARCHITECTURE.md "Manual serial paths"):
// it creates a real socat-linked PTY pair, opens one end through
// internal/serial.Open exactly as a saved manual-path device profile or
// `--port`/`--path` would, and verifies both directions transfer bytes
// exactly — including the embedded NUL and non-ASCII bytes that would
// break anything treating the stream as a C string or text. This is what
// proves internal/serial.Open never needed to know about PTYs specially:
// it opens whatever path it's given, hardware or virtual, unmodified.
//
// Skips (does not fail) when socat isn't installed — this is a real-OS
// integration test, not a substitute for the unit tests that don't need
// one. See scripts/pty-dev-test.sh for the equivalent manual/exploratory
// workflow with the actual `serialforge` binary.
func TestPTYRoundTrip(t *testing.T) {
	socatPath, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("socat not found in PATH — skipping PTY integration test (see scripts/pty-dev-test.sh)")
	}

	dir := t.TempDir()
	linkA := filepath.Join(dir, "serialforge-a")
	linkB := filepath.Join(dir, "serialforge-b")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, socatPath, "-d", "-d",
		"pty,raw,echo=0,link="+linkA,
		"pty,raw,echo=0,link="+linkB,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start socat: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		cmd.Wait() //nolint:errcheck // best-effort cleanup
	})

	if err := waitForFile(linkA, 3*time.Second); err != nil {
		t.Fatalf("socat did not create %s: %v", linkA, err)
	}
	if err := waitForFile(linkB, 3*time.Second); err != nil {
		t.Fatalf("socat did not create %s: %v", linkB, err)
	}

	// Side A: exactly what a manual-path device profile or `--port`/`--path`
	// resolves to and opens — the real transport under test.
	cfg := DefaultConfig()
	cfg.ReadTimeout = 2000
	portA, err := Open(linkA, cfg)
	if err != nil {
		t.Fatalf("Open(%s): %v", linkA, err)
	}
	defer portA.Close()

	// Side B: a plain file handle standing in for "another process" (per
	// the reported workflow: SerialForge <-> socat <-> a separate program),
	// deliberately not going through our own serial package on this side.
	fileB, err := os.OpenFile(linkB, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", linkB, err)
	}
	defer fileB.Close()

	// The exact bytes from the report: AA 55 02 00 C0 17 FF 00 80 — an
	// embedded 0x00 (would truncate a C string) and non-ASCII bytes
	// (0xC0/0x17/0xFF/0x80, would corrupt under any UTF-8/text handling).
	testBytes := []byte{0xAA, 0x55, 0x02, 0x00, 0xC0, 0x17, 0xFF, 0x00, 0x80}

	t.Run("TX_SerialForge_to_socat", func(t *testing.T) {
		n, err := portA.Write(testBytes)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if n != len(testBytes) {
			t.Fatalf("wrote %d bytes, want %d", n, len(testBytes))
		}
		got := make([]byte, len(testBytes))
		if err := readFullTimeout(fileB, got, 2*time.Second); err != nil {
			t.Fatalf("read on B: %v", err)
		}
		if !bytes.Equal(got, testBytes) {
			t.Fatalf("B received % X, want % X", got, testBytes)
		}
	})

	t.Run("RX_socat_to_SerialForge", func(t *testing.T) {
		if _, err := fileB.Write(testBytes); err != nil {
			t.Fatalf("write on B: %v", err)
		}
		got := make([]byte, len(testBytes))
		if err := readFullTimeout(portA, got, 2*time.Second); err != nil {
			t.Fatalf("Read on A: %v", err)
		}
		if !bytes.Equal(got, testBytes) {
			t.Fatalf("A received % X, want % X", got, testBytes)
		}
	})

	// Structured packet decoding over this same PTY is covered by
	// internal/session's PTY test (TestPTYSessionDecodesStructuredPacket) —
	// kept out of this package so internal/serial's tests stay scoped to
	// the raw transport and don't pull in internal/packet.
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Lstat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			_, err := os.Lstat(path)
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readFullTimeout reads exactly len(buf) bytes from r, retrying short reads
// (a PTY can deliver a write in more than one chunk) until buf is full or
// timeout elapses.
func readFullTimeout(r interface{ Read([]byte) (int, error) }, buf []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	filled := 0
	for filled < len(buf) {
		if time.Now().After(deadline) {
			return context.DeadlineExceeded
		}
		n, err := r.Read(buf[filled:])
		filled += n
		if err != nil && filled < len(buf) {
			// A real serial.Port with a read timeout returns (0, nil) on a
			// timeout rather than an error (see internal/serial.Config's
			// ReadTimeout doc) — but guard against a genuine error from the
			// raw os.File side too, retrying briefly rather than failing on
			// the first short read.
			time.Sleep(20 * time.Millisecond)
		}
	}
	return nil
}
