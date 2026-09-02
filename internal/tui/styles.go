// Package tui is the Bubble Tea application: one flat root model dispatching
// to per-tab view/update functions — a pragmatic single-model shape, not a
// nested-submodel-per-screen framework — a shared style palette, and the
// reusable register-style packet diagram every packet-aware screen renders
// through — see diagram.go's doc comment for why there is only one diagram
// implementation.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Shared style palette — deliberately the same numbered ANSI-256 colors
// throughout (see ARCHITECTURE.md "TUI") so every screen in the application
// reads as one coherent visual language rather than a patchwork.
//
// Color semantics (see ARCHITECTURE.md "TUI" / "Key-hint styling"):
//   - keyStyle (accent, 81): active tabs, selected controls, keyboard
//     keys/shortcuts, other important interactive affordances. Same color as
//     tabActive's background — the key-hint accent is deliberately the same
//     accent as the active tab, not a separate color.
//   - primaryStyle: labels, action descriptions, normal values — readable
//     content that must stay legible, never dimmed.
//   - secondaryStyle: offsets, metadata, subtle descriptions, separators —
//     genuinely secondary but still-available information.
//   - disabledStyle: actions that are genuinely unavailable right now.
//     secondaryStyle and disabledStyle happen to share color 240 today (it
//     already reads correctly for both), but are kept as two distinct named
//     styles rather than one shared variable, so retuning "how dim is
//     secondary metadata" can never accidentally also retune "how dim is a
//     disabled control", and vice versa.
var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("213")).Padding(0, 1)
	tabActive    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(lipgloss.Color("81")).Padding(0, 1)
	tabInactive  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Padding(0, 1)
	sectionStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	badStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	activeStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	keyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	boxStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	warnStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	amberStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	accentBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("213")).Padding(1, 3)

	// primaryStyle is readable, undimmed foreground text — action
	// descriptions in a key hint, and any other content that must never be
	// mistaken for disabled/secondary. Same color as fieldTextStyle's
	// diagram-specific role below (255, the brightest ordinary foreground
	// this palette uses) but named for its general-purpose use.
	primaryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	// secondaryStyle is dim, still-available metadata/separators — a named
	// alias of dimStyle's color, kept distinct from disabledStyle (see above).
	secondaryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	// disabledStyle marks a genuinely unavailable action/control — same
	// color as secondaryStyle today, kept as its own name (see above).
	disabledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// Diagram-specific roles, additional to the shared set above.
	crcTextStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")) // CRC span label/value
	fieldTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))            // ordinary field label/value
	unallocStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	selectedBorder = lipgloss.Color("81")
	normalBorder   = lipgloss.Color("240")
	crcBorder      = lipgloss.Color("214")
	unallocBorder  = lipgloss.Color("238")
)

const (
	glyphOK   = "✓"
	glyphBad  = "✗"
	glyphDot  = "●"
	glyphOpen = "○"
)

// KeyHint is one "key -> action" pair in a keyboard-hint bar — the single
// semantic unit every screen's help/footer line is built from. This is the
// one centralized key-hint primitive in the application: no screen should
// hand-assemble a raw "key description" string and wrap the whole thing in
// one style — see renderHints below and ARCHITECTURE.md "Key-hint styling".
type KeyHint struct {
	Key  string
	Desc string
	// Disabled marks a hint whose action isn't available right now (e.g.
	// "s Save" before anything has changed). A disabled hint renders both
	// the key and the description in disabledStyle instead of the normal
	// accent/primary split, so a user can tell at a glance it can't be used.
	Disabled bool
}

// hint is a small constructor for the common (non-disabled) case, so call
// sites read as a flat literal list: []KeyHint{hint("x", "Send"), ...}.
func hint(key, desc string) KeyHint {
	return KeyHint{Key: key, Desc: desc}
}

// renderHints joins one or more KeyHints into a single hint-bar string: the
// key token in the accent color (keyStyle), the action description in
// readable primary foreground (primaryStyle), separated by a dim middot
// (secondaryStyle) — this is the one place that visual hierarchy is defined
// for the whole application. A Disabled hint renders both halves in
// disabledStyle instead, so unavailable actions stay visually distinct from
// both normal hints and from secondary/metadata text.
func renderHints(hints ...KeyHint) string {
	if len(hints) == 0 {
		return ""
	}
	parts := make([]string, len(hints))
	for i, h := range hints {
		if h.Disabled {
			parts[i] = disabledStyle.Render(h.Key + " " + h.Desc)
			continue
		}
		parts[i] = keyStyle.Render(h.Key) + " " + primaryStyle.Render(h.Desc)
	}
	return strings.Join(parts, secondaryStyle.Render("  ·  "))
}
