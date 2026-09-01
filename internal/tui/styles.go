// Package tui is the Bubble Tea application: one flat root model dispatching
// to per-tab view/update functions — a pragmatic single-model shape, not a
// nested-submodel-per-screen framework — a shared style palette, and the
// reusable register-style packet diagram every packet-aware screen renders
// through — see diagram.go's doc comment for why there is only one diagram
// implementation.
package tui

import "github.com/charmbracelet/lipgloss"

// Shared style palette — deliberately the same numbered ANSI-256 colors
// throughout (see ARCHITECTURE.md "TUI") so every screen in the application
// reads as one coherent visual language rather than a patchwork.
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
