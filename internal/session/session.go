// Package session owns one open serial.Port plus a framing.Framer: it runs
// the RX goroutine, turns raw reads into framed Events, accepts TX writes,
// and (given an Opener) reconnects on cable-removal-style errors. Nothing
// above this package touches serial.Port directly — see ARCHITECTURE.md's
// "TUI code never touches an OS serial handle" invariant, which this
// package exists to make true.
package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// EventKind distinguishes what an Event carries.
type EventKind string

const (
	EventRX     EventKind = "rx"     // a complete frame was received
	EventTX     EventKind = "tx"     // Send() wrote data successfully
	EventStatus EventKind = "status" // connection lifecycle: see Status* constants
)

// Status values for EventStatus events.
const (
	StatusDisconnected = "disconnected"
	StatusReconnecting = "reconnecting"
	StatusReconnected  = "reconnected"
	StatusClosed       = "closed"
)

// Event is one item on a Session's Events channel.
type Event struct {
	Kind      EventKind
	Data      []byte // EventRX / EventTX
	Status    string // EventStatus
	Err       error  // set on disconnect / give-up
	Timestamp time.Time
}

// ReconnectPolicy controls whether and how a Session tries to reopen the
// port after a read error. Disabled (the zero value) means a read error
// ends the RX loop and emits one final EventStatus/StatusDisconnected.
type ReconnectPolicy struct {
	Enabled      bool
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Backoff      float64 // multiplier applied to the delay after each failed attempt
}

// DefaultReconnectPolicy is a reasonable starting point: 500ms initial
// delay, doubling up to a 10s ceiling.
func DefaultReconnectPolicy() ReconnectPolicy {
	return ReconnectPolicy{Enabled: true, InitialDelay: 500 * time.Millisecond, MaxDelay: 10 * time.Second, Backoff: 2}
}

// Opener reopens the underlying transport — typically serial.Open bound to
// a specific path/config, or (via internal/device) a profile's resolved
// path. nil disables reconnect regardless of ReconnectPolicy.Enabled.
type Opener func() (serial.Port, error)

// Config configures a new Session.
type Config struct {
	Port        serial.Port
	Framer      framing.Framer
	Opener      Opener
	Reconnect   ReconnectPolicy
	EventBuffer int // Events() channel capacity; <=0 defaults to 256
}

// Session is not safe for concurrent Start calls; Send and Close are safe
// to call from any goroutine while the RX loop (started by Start) is
// running.
type Session struct {
	mu     sync.Mutex
	port   serial.Port
	framer framing.Framer
	opener Opener
	policy ReconnectPolicy

	events chan Event
	cancel context.CancelFunc
	done   chan struct{}
}

// New constructs a Session around an already-open port. Start must be
// called to begin the RX loop.
func New(cfg Config) *Session {
	buf := cfg.EventBuffer
	if buf <= 0 {
		buf = 256
	}
	return &Session{
		port:   cfg.Port,
		framer: cfg.Framer,
		opener: cfg.Opener,
		policy: cfg.Reconnect,
		events: make(chan Event, buf),
		done:   make(chan struct{}),
	}
}

// Events returns the channel of RX/TX/status events. It is closed after the
// RX loop exits (following ctx cancellation or an unrecoverable error).
func (s *Session) Events() <-chan Event { return s.events }

// Start launches the RX goroutine. Cancel ctx (or call Close) to stop it.
func (s *Session) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	go s.run(ctx)
}

// Send writes data to the currently-open port and emits an EventTX on
// success. It does not itself trigger reconnect — a broken write surfaces
// through the RX loop's next read error, which does.
func (s *Session) Send(data []byte) (int, error) {
	s.mu.Lock()
	p := s.port
	s.mu.Unlock()
	if p == nil {
		return 0, errNotConnected
	}
	n, err := p.Write(data)
	if err == nil {
		s.emit(Event{Kind: EventTX, Data: append([]byte(nil), data[:n]...), Timestamp: now()})
	}
	return n, err
}

// Close cancels the RX loop, closes the underlying port, and waits for the
// goroutine to exit before returning — a clean shutdown, never a leaked
// reader goroutine blocked on a closed pipe.
func (s *Session) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	p := s.port
	s.mu.Unlock()
	var err error
	if p != nil {
		err = p.Close()
	}
	<-s.done
	return err
}

func (s *Session) currentPort() serial.Port {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

func (s *Session) setPort(p serial.Port) {
	s.mu.Lock()
	s.port = p
	s.mu.Unlock()
}

func (s *Session) emit(e Event) {
	select {
	case s.events <- e:
	default:
		// Events channel is full (a very slow/stuck consumer): drop rather
		// than block the RX loop and stall reading from the wire — the
		// serial reader must never block because the TUI is slow.
	}
}

func (s *Session) run(ctx context.Context) {
	defer close(s.done)
	defer close(s.events)

	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return
		}
		p := s.currentPort()
		n, err := p.Read(buf)
		if n > 0 {
			s.framer.Push(buf[:n])
			for {
				frame, ok := s.framer.Next()
				if !ok {
					break
				}
				s.emit(Event{Kind: EventRX, Data: frame, Timestamp: now()})
			}
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return // Close()/ctx cancellation caused the read to unblock/error; not a real disconnect
		}
		if !s.reconnect(ctx, err) {
			return
		}
	}
}

// reconnect handles one read error: if reconnect is disabled or no Opener
// was given, it reports the disconnect and stops the loop (returns false).
// Otherwise it retries Opener with exponential backoff until it succeeds or
// ctx is cancelled.
func (s *Session) reconnect(ctx context.Context, cause error) bool {
	s.emit(Event{Kind: EventStatus, Status: StatusDisconnected, Err: cause, Timestamp: now()})
	if !s.policy.Enabled || s.opener == nil {
		return false
	}
	s.framer.Reset()

	delay := s.policy.InitialDelay
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	maxDelay := s.policy.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 10 * time.Second
	}
	backoff := s.policy.Backoff
	if backoff <= 1 {
		backoff = 2
	}

	for {
		s.emit(Event{Kind: EventStatus, Status: StatusReconnecting, Timestamp: now()})
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
		p, err := s.opener()
		if err == nil {
			s.setPort(p)
			s.emit(Event{Kind: EventStatus, Status: StatusReconnected, Timestamp: now()})
			return true
		}
		delay = time.Duration(float64(delay) * backoff)
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

var errNotConnected = errors.New("session: no port open")

// now is a var so tests could override it if ever needed; kept simple
// (time.Now) rather than threading a clock through Config for v0.1.
var now = time.Now
