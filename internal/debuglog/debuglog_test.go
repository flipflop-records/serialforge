package debuglog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDisabledByDefaultNoFileWritten(t *testing.T) {
	t.Setenv("SERIALFORGE_DEBUG_LOG", "")
	closeFn := Init()
	defer closeFn()

	if Enabled() {
		t.Fatal("Enabled() should be false with SERIALFORGE_DEBUG_LOG unset")
	}
	// Must not panic or block with logging disabled.
	Event("key", "key", "q", "route", "global_quit")
}

func TestEnabledWritesExpectedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug.log")
	t.Setenv("SERIALFORGE_DEBUG_LOG", path)
	closeFn := Init()
	defer closeFn()

	if !Enabled() {
		t.Fatal("Enabled() should be true once SERIALFORGE_DEBUG_LOG names a writable path")
	}
	Event("key", "key", "q", "tab", "Monitor", "pane", "Saved", "editing", false, "route", "global_quit")
	closeFn()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	line := string(data)
	for _, want := range []string{"key ", "key=q", "tab=Monitor", "pane=Saved", "editing=false", "route=global_quit"} {
		if !strings.Contains(line, want) {
			t.Errorf("log line missing %q, got: %s", want, line)
		}
	}
	// Timestamp prefix, HH:MM:SS.mmm.
	if len(line) < 12 || line[2] != ':' || line[5] != ':' {
		t.Errorf("expected an HH:MM:SS.mmm timestamp prefix, got: %s", line)
	}
}

func TestInitTruncatesPreviousRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug.log")
	if err := os.WriteFile(path, []byte("stale content from a previous run\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERIALFORGE_DEBUG_LOG", path)
	closeFn := Init()
	Event("test", "k", "v")
	closeFn()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "stale content") {
		t.Errorf("Init should truncate a previous run's log, got: %s", data)
	}
}

func TestInitUnwritablePathStaysDisabled(t *testing.T) {
	t.Setenv("SERIALFORGE_DEBUG_LOG", "/nonexistent-dir-xyz/debug.log")
	closeFn := Init()
	defer closeFn()

	if Enabled() {
		t.Fatal("Enabled() should be false when the path can't be opened")
	}
	// Must not panic.
	Event("key", "key", "q")
}

func TestFormatValHexBytesAndQuoting(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{[]byte{0x01, 0x02, 0xDE, 0xAD}, `"01 02 DE AD"`},
		{[]byte{}, `""`},
		{"plain", "plain"},
		{"has space", `"has space"`},
		{"", `""`},
		{true, "true"},
		{false, "false"},
		{42, "42"},
	}
	for _, c := range cases {
		if got := formatVal(c.in); got != c.want {
			t.Errorf("formatVal(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEventOddKVArgsDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug.log")
	t.Setenv("SERIALFORGE_DEBUG_LOG", path)
	closeFn := Init()
	defer closeFn()

	Event("weird", "onlyKey") // odd number of kv args
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "!odd_kv_args") {
		t.Errorf("expected a marker for malformed kv args, got: %s", data)
	}
}

func TestEventConcurrentSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug.log")
	t.Setenv("SERIALFORGE_DEBUG_LOG", path)
	closeFn := Init()
	defer closeFn()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			Event("concurrent", "n", n)
		}(i)
	}
	wg.Wait()
	closeFn()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "concurrent"); got != 50 {
		t.Errorf("expected 50 concurrent log lines, got %d", got)
	}
}
