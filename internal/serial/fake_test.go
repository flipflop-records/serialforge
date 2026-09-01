package serial

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestFakePortRoundTrip(t *testing.T) {
	port, dev := NewFakePort()
	defer port.Close()
	defer dev.Close()

	go func() { dev.Write([]byte("hello")) }()

	buf := make([]byte, 5)
	n, err := io.ReadFull(port, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !bytes.Equal(buf[:n], []byte("hello")) {
		t.Fatalf("got %q, want %q", buf[:n], "hello")
	}
}

func TestFakePortWriteVisibleToDevice(t *testing.T) {
	port, dev := NewFakePort()
	defer port.Close()
	defer dev.Close()

	go func() { port.Write([]byte("AA55")) }()

	buf := make([]byte, 4)
	n, err := io.ReadFull(dev, buf)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !bytes.Equal(buf[:n], []byte("AA55")) {
		t.Fatalf("got %q, want %q", buf[:n], "AA55")
	}
}

func TestFakePortCloseUnblocksRead(t *testing.T) {
	port, dev := NewFakePort()
	defer dev.Close()

	done := make(chan error, 1)
	go func() {
		_, err := port.Read(make([]byte, 1))
		done <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the goroutine block on Read
	if err := port.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read returned nil error after Close, want io.ErrClosedPipe/EOF")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not unblock within 2s of Close — simulated cable removal must not hang the reader")
	}
}

func TestFakePortDoubleCloseIsSafe(t *testing.T) {
	port, dev := NewFakePort()
	defer dev.Close()
	if err := port.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := port.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestFakePortSetReadTimeoutIsNoop(t *testing.T) {
	port, dev := NewFakePort()
	defer port.Close()
	defer dev.Close()
	if err := port.SetReadTimeout(500); err != nil {
		t.Fatalf("SetReadTimeout: %v", err)
	}
}
