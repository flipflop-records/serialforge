// Package debuglog is a small, opt-in, file-based diagnostic logger for
// development use — tracing TUI key routing, protocol activation, session
// lifecycle, and TX/RX traffic while investigating a hard-to-reproduce bug.
//
// Disabled by default: normal users never see any output, and there is no
// runtime cost beyond one atomic bool check per call site when disabled.
// Enabled by setting SERIALFORGE_DEBUG_LOG to a file path before starting
// the TUI; every subsequent Event call appends one line to that file.
//
// This is diagnostic-only, never load-bearing: a failure to open or write
// the log file is silently ignored (Init falls back to disabled; a write
// error just drops that line) — SerialForge's own behavior must never
// depend on whether logging is enabled or succeeding. Output goes only to
// the file, never to stdout/stderr, so it can never corrupt Bubble Tea's
// alt-screen rendering. Safe for concurrent use from multiple goroutines
// (the session's own event-pump goroutine logs RX independently of the
// main TUI goroutine logging keys/TX).
package debuglog

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	file    *os.File
	enabled bool
)

// Init enables the logger if SERIALFORGE_DEBUG_LOG names a writable path,
// truncating any previous run's content — each TUI session starts its own
// clean log. Safe to call once at startup; a no-op (logging stays
// disabled) if the env var is unset or the file can't be opened. Returns a
// close function the caller should defer (also safe to call when logging
// was never enabled).
func Init() (closeFn func()) {
	path := os.Getenv("SERIALFORGE_DEBUG_LOG")
	if path == "" {
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		// Diagnostic logging failing to open must never stop the app —
		// just stay disabled.
		return func() {}
	}
	mu.Lock()
	file = f
	enabled = true
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		enabled = false
		if file != nil {
			_ = file.Close()
			file = nil
		}
	}
}

// Enabled reports whether logging is currently active — callers with an
// expensive-to-format argument (e.g. hex-encoding a large buffer) can
// check this first to skip that work entirely when disabled.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// Event appends one line: "HH:MM:SS.mmm <kind> k1=v1 k2=v2 ...". kv is an
// alternating key/value list (not a map) so field order is deterministic
// and matches each call site's own reading order — important for a
// grep/diff-friendly trace. Values are formatted by formatVal: strings
// containing whitespace are quoted, []byte becomes compact space-separated
// hex, bools/numbers/errors print plainly. A malformed (odd-length) kv is
// a programmer error caught by the trailing note appended for that call,
// not a panic — logging must never crash the app.
func Event(kind string, kv ...any) {
	if !Enabled() {
		return
	}
	var b strings.Builder
	b.WriteString(time.Now().Format("15:04:05.000"))
	b.WriteByte(' ')
	b.WriteString(kind)
	for i := 0; i+1 < len(kv); i += 2 {
		key, _ := kv[i].(string)
		fmt.Fprintf(&b, " %s=%s", key, formatVal(kv[i+1]))
	}
	if len(kv)%2 != 0 {
		b.WriteString(" !odd_kv_args")
	}
	b.WriteByte('\n')

	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	_, _ = file.WriteString(b.String()) // best-effort; a write failure is never fatal
}

func formatVal(v any) string {
	switch x := v.(type) {
	case []byte:
		return hexCompact(x)
	case string:
		if x == "" || strings.ContainsAny(x, " \t\"") {
			return fmt.Sprintf("%q", x)
		}
		return x
	case error:
		if x == nil {
			return "nil"
		}
		return fmt.Sprintf("%q", x.Error())
	case fmt.Stringer:
		return formatVal(x.String())
	default:
		return fmt.Sprint(x)
	}
}

func hexCompact(b []byte) string {
	if len(b) == 0 {
		return `""`
	}
	var sb strings.Builder
	sb.WriteByte('"')
	for i, c := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02X", c)
	}
	sb.WriteByte('"')
	return sb.String()
}
