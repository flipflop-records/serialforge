package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func symlinkTo(t *testing.T, dir, name, target string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink %s -> %s: %v", path, target, err)
	}
	return path
}

// TestVirtualChooserOpensOnM is the regression test for the UX fix itself:
// 'm' on the Devices tab must open the candidate chooser, NOT immediately
// drop the user into a raw path-entry field — typing a path is the
// fallback, reached only via the chooser's "Enter custom path..." row.
func TestVirtualChooserOpensOnM(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabDevices

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.devVirtual == nil {
		t.Fatal("'m' on the Devices tab should open the Virtual/Manual chooser")
	}
	if m.devManual != nil {
		t.Error("'m' must not jump straight to the manual path-entry form")
	}
	if out := m.View(); !strings.Contains(out, "Virtual / Manual endpoints") {
		t.Errorf("Devices view should show the chooser, got:\n%s", out)
	}
}

func TestVirtualChooserEscCancels(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabDevices
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.devVirtual == nil {
		t.Fatal("expected the chooser to open")
	}
	pressKey(m, tea.KeyEsc)
	if m.devVirtual != nil {
		t.Error("esc should close the chooser without connecting")
	}
}

// TestVirtualChooserCustomPathFallsThroughToManualForm: navigating to and
// selecting "Enter custom path..." (always the last row) must open the
// existing manual text-entry flow — the documented fallback path, still
// fully supported.
func TestVirtualChooserCustomPathFallsThroughToManualForm(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabDevices
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.devVirtual == nil {
		t.Fatal("expected the chooser to open")
	}
	// Jump to the last (always-present) selectable row: "Enter custom path...".
	m.devVirtual.pos = len(m.devVirtual.selectable) - 1
	pressKey(m, tea.KeyEnter)

	if m.devVirtual != nil {
		t.Error("chooser should close once 'Enter custom path...' is selected")
	}
	if m.devManual == nil {
		t.Fatal("selecting 'Enter custom path...' should open the manual-connect form")
	}
	if out := m.View(); !strings.Contains(out, "Manual connect") {
		t.Errorf("Devices view should show the manual-connect form, got:\n%s", out)
	}

	// The manual form itself still works exactly as before: enter a path,
	// attempt to connect (this path doesn't exist, so it's expected to
	// fail cleanly, not crash or go silent).
	typeString(m, "/tmp/serialforge-does-not-exist")
	pressKey(m, tea.KeyTab)
	typeString(m, "115200")
	pressKey(m, tea.KeyEnter)

	if m.devManual != nil {
		t.Error("manual-connect form should close after submitting")
	}
	if m.status == "" {
		t.Error("submitting a manual connect (even one that fails) should leave a status message, not silence")
	}
	if m.quit {
		t.Error("a failed manual connect must not quit the TUI")
	}
}

// TestVirtualChooserShowsDiscoveredSymlink is the end-to-end regression
// case for the reported workflow: a socat-style friendly symlink
// (simulated here — see internal/serial/pty_test.go for the real-socat
// integration coverage) must appear in the chooser, grouped under
// "Friendly symlinks", selectable and connectable without ever typing its
// path — and selecting it must record it in recent history.
func TestVirtualChooserShowsDiscoveredSymlink(t *testing.T) {
	dir := t.TempDir()
	a := symlinkTo(t, dir, "serialforge-a", "/dev/ttys004")

	m := newTestModel(t)
	m.tab = tabDevices
	overrideSymlinkDirsForTest(t, m, []string{dir})

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.devVirtual == nil {
		t.Fatal("expected the chooser to open")
	}
	out := m.View()
	// The row is shown truncated-keeping-tail for long paths (t.TempDir()
	// paths are long) — assert on the basename, not the full path; see
	// TestTruncatePathKeepingTail for the truncation behavior itself.
	if !strings.Contains(out, "Friendly symlinks") || !strings.Contains(out, "serialforge-a") {
		t.Fatalf("chooser should list the discovered symlink under 'Friendly symlinks', got:\n%s", out)
	}

	// Select it (it's the first, and in this test the only, candidate).
	m.devVirtual.pos = 0
	pressKey(m, tea.KeyEnter)

	if m.devVirtual != nil {
		t.Error("chooser should close after selecting a candidate")
	}
	if m.status == "" {
		t.Error("connecting (even to a fake target that fails at the OS level) should leave a status message")
	}
	if m.recent == nil {
		t.Fatal("test setup: expected a recent store")
	}
	found := false
	for _, e := range m.recent.All() {
		if e.Path == a {
			found = true
		}
	}
	if !found {
		t.Error("selecting a candidate should record it in recent history, even if the connect attempt itself fails")
	}
}

func TestVirtualChooserEmptyStateShowsCustomPathAction(t *testing.T) {
	dir := t.TempDir() // empty — no symlinks
	m := newTestModel(t)
	m.tab = tabDevices
	overrideSymlinkDirsForTest(t, m, []string{dir})

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	if m.devVirtual == nil {
		t.Fatal("expected the chooser to open")
	}
	if len(m.devVirtual.selectable) != 1 {
		t.Fatalf("empty state should have exactly one selectable row (the custom-path fallback), got %d", len(m.devVirtual.selectable))
	}
	out := m.View()
	if !strings.Contains(out, "No virtual/manual endpoints found") {
		t.Errorf("expected an explicit empty-state message, got:\n%s", out)
	}
	if !strings.Contains(out, "Enter custom path...") {
		t.Errorf("the custom-path fallback must always be present, got:\n%s", out)
	}
}

// overrideSymlinkDirsForTest reopens the chooser with a caller-controlled
// symlink search path instead of the real /tmp, so tests never depend on
// what happens to exist on the developer's machine.
func overrideSymlinkDirsForTest(t *testing.T, m *model, dirs []string) {
	t.Helper()
	origBuild := buildVirtualChooserFunc
	buildVirtualChooserFunc = func(m *model) *virtualChooserState {
		return newVirtualChooserWithDirs(m, dirs)
	}
	t.Cleanup(func() { buildVirtualChooserFunc = origBuild })
}

// TestAddDeviceFormAcceptsPathWithoutIdentity: saving a profile with only
// an alias + manual path (no VID/PID) must work end to end through the TUI
// form, matching device.Resolve's "path-only profile" support.
func TestAddDeviceFormAcceptsPathWithoutIdentity(t *testing.T) {
	m := newTestModel(t)
	m.tab = tabDevices
	// Deterministic: with real detected ports present (this host's actual
	// serial devices), the cursor's default position can coincide with one
	// and 'a' prefills its metadata by design (see updateDevices) — force
	// zero detected ports so this test's input isn't appended to a prefill.
	m.detected = nil

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.devAdd == nil {
		t.Fatal("'a' should open the add-device form")
	}

	typeString(m, "virtual") // Alias
	pressKey(m, tea.KeyTab)
	typeString(m, "/tmp/serialforge-a") // Path
	// Skip VID/PID/Serial — go straight to submitting via repeated Tab+Enter.
	pressKey(m, tea.KeyTab) // VID
	pressKey(m, tea.KeyTab) // PID
	pressKey(m, tea.KeyTab) // Serial number
	pressKey(m, tea.KeyTab) // Baud
	pressKey(m, tea.KeyEnter)

	if m.devAdd != nil {
		t.Fatal("form should have closed after submitting on the last field")
	}
	p, ok := m.devices.Get("virtual")
	if !ok {
		t.Fatal("profile was not saved")
	}
	if p.Path != "/tmp/serialforge-a" {
		t.Errorf("Path = %q, want /tmp/serialforge-a", p.Path)
	}
	if p.VID != "" || p.PID != "" {
		t.Errorf("expected no VID/PID for a path-only profile, got VID=%q PID=%q", p.VID, p.PID)
	}
}
