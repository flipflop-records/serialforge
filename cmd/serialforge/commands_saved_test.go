package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/protocol"
	"github.com/vtemnyakov/serialforge/internal/savedpacket"
)

// savedTestSchema mirrors the small fixed schema other CLI tests in this
// package use — a tiny, deterministic packet cheap to hand-verify.
func savedTestSchema() packet.Schema {
	return packet.Schema{
		Name:      "demo",
		TotalSize: 4,
		Fields: []packet.Field{
			{Name: "command", Size: 1, Format: packet.FormatUint},
			{Name: "value", Size: 2, Endianness: packet.BigEndian, Format: packet.FormatHex},
		},
		Checksum: checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8"},
	}
}

func setupSavedConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	protocols, err := protocol.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocols.Put(savedTestSchema()); err != nil {
		t.Fatal(err)
	}
	if err := protocols.Save(); err != nil {
		t.Fatal(err)
	}
	saved, err := savedpacket.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	sp := savedpacket.SavedPacket{
		Name: "get-status", Protocol: "demo",
		Values:  map[string]string{"command": "02", "value": "00FF"},
		CRCMode: savedpacket.CRCModeAuto,
		Hotkey:  "'",
	}
	if err := saved.Put(sp); err != nil {
		t.Fatal(err)
	}
	if err := saved.Save(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCmdSavedListShowDelete(t *testing.T) {
	dir := setupSavedConfigDir(t)

	if err := run([]string{"saved", "list", "--config", dir}); err != nil {
		t.Fatalf("saved list: %v", err)
	}
	if err := run([]string{"saved", "show", "get-status", "--config", dir}); err != nil {
		t.Fatalf("saved show: %v", err)
	}
	if err := run([]string{"saved", "show", "does-not-exist", "--config", dir}); err == nil {
		t.Fatal("saved show for an unknown name should error")
	}

	saved, err := savedpacket.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Get("get-status"); !ok {
		t.Fatal("test setup: expected get-status to exist before delete")
	}
	if err := run([]string{"saved", "delete", "get-status", "--config", dir}); err != nil {
		t.Fatalf("saved delete: %v", err)
	}
	saved2, err := savedpacket.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := saved2.Get("get-status"); ok {
		t.Fatal("saved packet should be gone after delete")
	}
}

func TestCmdSavedSendArgvOrderIndependent(t *testing.T) {
	socatPath, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("socat not found in PATH — skipping saved-send PTY integration test")
	}
	dir := setupSavedConfigDir(t)

	pty := filepath.Join(t.TempDir(), "serialforge-cli-a")
	ptyB := filepath.Join(t.TempDir(), "serialforge-cli-b")

	cmd := exec.Command(socatPath, "-d", "-d",
		"pty,raw,echo=0,link="+pty,
		"pty,raw,echo=0,link="+ptyB,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start socat: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() }) //nolint:errcheck

	if err := waitForFileExists(pty, 3*time.Second); err != nil {
		t.Fatalf("socat did not create %s: %v", pty, err)
	}
	if err := waitForFileExists(ptyB, 3*time.Second); err != nil {
		t.Fatalf("socat did not create %s: %v", ptyB, err)
	}

	fileB, err := os.OpenFile(ptyB, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", ptyB, err)
	}
	defer fileB.Close()

	protocols, _ := protocol.Load(dir)
	saved, _ := savedpacket.Load(dir)
	sp, _ := saved.Get("get-status")
	want, err := sp.Build(protocols)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Two argv shapes for the exact same send — flags in different order,
	// plus the positional-shorthand form — must all resolve identically
	// (the CLI argument-parsing invariant every other command honors).
	argvVariants := [][]string{
		{"saved", "send", "get-status", "--port", pty, "--config", dir},
		{"saved", "send", "get-status", "--config", dir, "--port", pty},
		{"saved", "send", "get-status", pty, "--config", dir},
	}
	for i, argv := range argvVariants {
		if err := run(argv); err != nil {
			t.Fatalf("variant %d (%v): %v", i, argv, err)
		}
		got := make([]byte, len(want.Raw))
		if err := readFullTimeout(fileB, got, 2*time.Second); err != nil {
			t.Fatalf("variant %d: read: %v", i, err)
		}
		if !bytes.Equal(got, want.Raw) {
			t.Fatalf("variant %d: got % X, want % X", i, got, want.Raw)
		}
	}
}

func waitForFileExists(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(path); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err := os.Lstat(path)
	return err
}

func readFullTimeout(r interface{ Read([]byte) (int, error) }, buf []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	got := 0
	for got < len(buf) {
		if time.Now().After(deadline) {
			return os.ErrDeadlineExceeded
		}
		n, err := r.Read(buf[got:])
		got += n
		if err != nil && got < len(buf) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}
