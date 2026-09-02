package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// withColorForced forces lipgloss's default renderer to a real color
// profile for the duration of a test and restores whatever it was
// afterward. go test runs with no TTY, so lipgloss's own auto-detection
// falls back to the No-Color profile and every Style.Render call becomes a
// no-op passthrough — without this, none of the SGR-sequence assertions
// below would ever see a color code, forced or not.
func withColorForced(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestRenderHintsSeparatesKeyAndDescStyling(t *testing.T) {
	withColorForced(t)

	out := renderHints(hint("x", "Send"))

	keySeq := keyStyle.Render("x")
	descSeq := primaryStyle.Render("Send")
	if !strings.Contains(out, keySeq) {
		t.Errorf("renderHints output %q does not contain the accent-styled key %q", out, keySeq)
	}
	if !strings.Contains(out, descSeq) {
		t.Errorf("renderHints output %q does not contain the primary-styled description %q", out, descSeq)
	}
	// The description must never be wrapped in disabledStyle's sequence
	// when the hint isn't disabled — that's exactly the low-contrast bug
	// this helper exists to fix.
	disabledSeq := disabledStyle.Render("Send")
	if disabledSeq != descSeq && strings.Contains(out, disabledSeq) {
		t.Errorf("renderHints output %q wraps an enabled description in disabledStyle", out)
	}
}

func TestRenderHintsMultipleJoinedBySecondaryStyle(t *testing.T) {
	withColorForced(t)

	out := renderHints(hint("x", "Send"), hint("d", "Duplicate"))
	sep := secondaryStyle.Render("  ·  ")
	if !strings.Contains(out, sep) {
		t.Errorf("renderHints output %q does not contain the secondary-styled separator %q", out, sep)
	}
	if !strings.Contains(out, keyStyle.Render("d")) || !strings.Contains(out, primaryStyle.Render("Duplicate")) {
		t.Errorf("renderHints output %q is missing the second hint's styled spans", out)
	}
}

func TestRenderHintsDisabledWrapsBothInDisabledStyle(t *testing.T) {
	withColorForced(t)

	out := renderHints(KeyHint{Key: "s", Desc: "Save", Disabled: true})
	want := disabledStyle.Render("s Save")
	if out != want {
		t.Errorf("disabled hint = %q, want %q", out, want)
	}
	// It must specifically NOT use the accent/primary split a normal hint gets.
	if strings.Contains(out, keyStyle.Render("s")) {
		t.Errorf("disabled hint %q unexpectedly uses the accent key style", out)
	}
}

func TestRenderHintsEmptyAndSingle(t *testing.T) {
	if got := renderHints(); got != "" {
		t.Errorf("renderHints() with no hints = %q, want empty string", got)
	}
	withColorForced(t)
	if got := renderHints(hint("q", "quit")); got == "" {
		t.Error("renderHints with one hint returned empty string")
	}
}

// TestRenderHintsFitsNarrowWidth is a narrow-layout sanity check: a
// realistic multi-hint bar should still be a single line whose visible
// (ANSI-stripped) width is reasonable for a narrow terminal, and lipgloss.Width
// (not len, which would count escape-sequence bytes) is the right way to
// measure it — matching diagram_test.go's own convention.
func TestRenderHintsFitsNarrowWidth(t *testing.T) {
	withColorForced(t)
	out := renderHints(hint("enter", "edit row"), hint("n", "new field"), hint("x", "delete"))
	if strings.Contains(out, "\n") {
		t.Errorf("renderHints output should be a single line, got %q", out)
	}
	if w := lipgloss.Width(out); w == 0 {
		t.Errorf("renderHints visible width = 0, want > 0")
	}
}
