package session

import (
	"context"
	"testing"
	"time"

	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

func newLineFramer(t *testing.T) framing.Framer {
	t.Helper()
	f, err := framing.New(framing.KindLine, framing.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func waitEvent(t *testing.T, events <-chan Event, kind EventKind, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				t.Fatalf("events channel closed while waiting for %s", kind)
			}
			if e.Kind == kind {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event kind %s", kind)
		}
	}
}

func TestSessionDeliversFramedRXEvents(t *testing.T) {
	port, dev := serial.NewFakePort()
	defer dev.Close()

	s := New(Config{Port: port, Framer: newLineFramer(t)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Close()

	dev.Write([]byte("hello\nworld\n"))

	e1 := waitEvent(t, s.Events(), EventRX, 2*time.Second)
	if string(e1.Data) != "hello" {
		t.Fatalf("first frame = %q, want %q", e1.Data, "hello")
	}
	e2 := waitEvent(t, s.Events(), EventRX, 2*time.Second)
	if string(e2.Data) != "world" {
		t.Fatalf("second frame = %q, want %q", e2.Data, "world")
	}
}

func TestSessionSendEmitsTXEvent(t *testing.T) {
	port, dev := serial.NewFakePort()
	defer dev.Close()

	s := New(Config{Port: port, Framer: newLineFramer(t)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Close()

	// FakePort.Write blocks until something reads the FakeDevice side (no
	// OS driver buffer to absorb it — see FakePort's doc comment), so the
	// device-side read must be running concurrently with Send.
	sendErr := make(chan error, 1)
	go func() {
		_, err := s.Send([]byte("ping\n"))
		sendErr <- err
	}()

	buf := make([]byte, 5)
	n, err := dev.Read(buf)
	if err != nil {
		t.Fatalf("device read: %v", err)
	}
	if string(buf[:n]) != "ping\n" {
		t.Fatalf("device saw %q, want %q", buf[:n], "ping\n")
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("Send: %v", err)
	}

	tx := waitEvent(t, s.Events(), EventTX, 2*time.Second)
	if string(tx.Data) != "ping\n" {
		t.Fatalf("TX event data = %q, want %q", tx.Data, "ping\n")
	}
}

func TestSessionCloseStopsCleanlyAndClosesEvents(t *testing.T) {
	port, dev := serial.NewFakePort()
	defer dev.Close()

	s := New(Config{Port: port, Framer: newLineFramer(t)})
	s.Start(context.Background())

	done := make(chan struct{})
	go func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not return within 2s")
	}

	// Events channel must be closed after the RX loop exits.
	select {
	case _, ok := <-s.Events():
		if ok {
			t.Fatal("Events() produced a value after Close(); expected the channel to just be closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Events() channel was not closed within 2s of Close()")
	}
}

func TestSessionReconnectsAndResumesRX(t *testing.T) {
	port1, dev1 := serial.NewFakePort()
	port2, dev2 := serial.NewFakePort()
	defer dev2.Close()

	opened := make(chan struct{}, 1)
	s := New(Config{
		Port:   port1,
		Framer: newLineFramer(t),
		Opener: func() (serial.Port, error) {
			opened <- struct{}{}
			return port2, nil
		},
		Reconnect: ReconnectPolicy{Enabled: true, InitialDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond, Backoff: 2},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Close()

	// Simulate cable removal: the device side closes, which makes port1's
	// pending Read return an error.
	dev1.Close()

	waitEvent(t, s.Events(), EventStatus, 2*time.Second) // disconnected

	select {
	case <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("Opener was not called after disconnect")
	}

	reconnected := waitEvent(t, s.Events(), EventStatus, 2*time.Second)
	// Drain any interleaved "reconnecting" status events until we see the
	// terminal one.
	for reconnected.Status != StatusReconnected {
		reconnected = waitEvent(t, s.Events(), EventStatus, 2*time.Second)
	}

	// RX should now flow over port2.
	dev2.Write([]byte("resumed\n"))
	rx := waitEvent(t, s.Events(), EventRX, 2*time.Second)
	if string(rx.Data) != "resumed" {
		t.Fatalf("post-reconnect RX = %q, want %q", rx.Data, "resumed")
	}
}
