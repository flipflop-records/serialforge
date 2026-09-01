//go:build !windows

package session

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// TestPTYSessionDecodesStructuredPacket is the structured-packet half of
// the manual-serial-path PTY integration test (see
// internal/serial/pty_test.go for the raw-byte-fidelity half): a real
// socat-linked PTY pair, opened through the exact Session+Framer stack the
// TUI/CLI use, decoding a packet whose bytes include an embedded NUL and
// non-ASCII bytes — proving binary safety survives through framing and
// packet.Decode, not just the raw transport.
//
// Skips (does not fail) when socat isn't installed.
func TestPTYSessionDecodesStructuredPacket(t *testing.T) {
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

	// schema: 2B header, 1B command, 4B address, 2B trailer, CRC-8 tail —
	// 9 bytes total, matching the report's exact test bytes.
	schema := packet.Schema{
		Name:      "pty-test",
		TotalSize: 9,
		Fields: []packet.Field{
			{Name: "header", Size: 2, Format: packet.FormatHex},
			{Name: "command", Size: 1, Format: packet.FormatUint},
			{Name: "address", Size: 4, Format: packet.FormatHex},
			{Name: "trailer", Size: 1, Format: packet.FormatRaw},
		},
		Checksum: checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/MAXIM-DOW"},
	}
	if err := schema.Validate(); err != nil {
		t.Fatalf("test schema invalid: %v", err)
	}

	values := packet.Values{
		"header":  {0xAA, 0x55},
		"command": {0x02},
		"address": {0x00, 0xC0, 0x17, 0xFF},
		"trailer": {0x00}, // embedded NUL — the exact byte class this test exists to protect
	}
	raw, crc, err := packet.Serialize(schema, values, nil)
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if !crc.Valid {
		t.Fatalf("test setup: expected a valid AUTO CRC, got %+v", crc)
	}

	cfg := serial.DefaultConfig()
	cfg.ReadTimeout = 2000
	portA, err := serial.Open(linkA, cfg)
	if err != nil {
		t.Fatalf("Open(%s): %v", linkA, err)
	}

	framer, err := framing.New(framing.KindFixed, framing.Options{Size: schema.TotalSize})
	if err != nil {
		t.Fatalf("framing.New: %v", err)
	}
	sess := New(Config{Port: portA, Framer: framer})
	sess.Start(ctx)
	t.Cleanup(func() { sess.Close() })

	fileB, err := os.OpenFile(linkB, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", linkB, err)
	}
	defer fileB.Close()

	if _, err := fileB.Write(raw); err != nil {
		t.Fatalf("write on B: %v", err)
	}

	select {
	case e, ok := <-sess.Events():
		if !ok {
			t.Fatal("session closed before delivering the frame")
		}
		if e.Kind != EventRX {
			t.Fatalf("first event = %v, want EventRX", e.Kind)
		}
		if !bytes.Equal(e.Data, raw) {
			t.Fatalf("frame = % X, want % X (raw bytes must survive framing exactly, embedded NUL included)", e.Data, raw)
		}
		pkt, err := packet.Decode(schema, e.Data)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !pkt.CRC.Valid {
			t.Errorf("decoded CRC = %+v, want valid", pkt.CRC)
		}
		if pkt.Fields[0].Uint != 0xAA55 {
			t.Errorf("header = 0x%X, want 0xAA55", pkt.Fields[0].Uint)
		}
		if pkt.Fields[2].Uint != 0xC017FF {
			t.Errorf("address = 0x%X, want 0xC017FF", pkt.Fields[2].Uint)
		}
		if !bytes.Equal(pkt.Fields[3].Raw, []byte{0x00}) {
			t.Errorf("trailer raw = % X, want 00 (embedded NUL preserved through Session+Decode)", pkt.Fields[3].Raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the session to deliver the RX frame")
	}
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
