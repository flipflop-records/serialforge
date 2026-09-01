// Package device implements persistent device aliases ("smart device
// profiles" — product spec §5): a name like "fpga" that resolves to a
// physical serial port by USB identity rather than a possibly-renumbered
// OS path, plus the profile's default serial.Config.
package device

import (
	"fmt"
	"strings"

	"github.com/vtemnyakov/serialforge/internal/serial"
)

// Profile is one saved device alias.
type Profile struct {
	Alias        string `yaml:"alias" json:"alias"`
	VID          string `yaml:"vid,omitempty" json:"vid,omitempty"`
	PID          string `yaml:"pid,omitempty" json:"pid,omitempty"`
	SerialNumber string `yaml:"serial_number,omitempty" json:"serial_number,omitempty"`
	Manufacturer string `yaml:"manufacturer,omitempty" json:"manufacturer,omitempty"`
	Product      string `yaml:"product,omitempty" json:"product,omitempty"`
	Path         string `yaml:"path,omitempty" json:"path,omitempty"` // fallback / hint, not authoritative

	Baud        int                `yaml:"baud,omitempty" json:"baud,omitempty"`
	DataBits    int                `yaml:"data_bits,omitempty" json:"data_bits,omitempty"`
	Parity      serial.Parity      `yaml:"parity,omitempty" json:"parity,omitempty"`
	StopBits    serial.StopBits    `yaml:"stop_bits,omitempty" json:"stop_bits,omitempty"`
	FlowControl serial.FlowControl `yaml:"flow_control,omitempty" json:"flow_control,omitempty"`

	DefaultProtocol string `yaml:"default_protocol,omitempty" json:"default_protocol,omitempty"`
}

// SerialConfig returns the profile's serial.Config, filling any zero field
// from serial.DefaultConfig().
func (p Profile) SerialConfig() serial.Config {
	c := serial.DefaultConfig()
	if p.Baud != 0 {
		c.Baud = p.Baud
	}
	if p.DataBits != 0 {
		c.DataBits = p.DataBits
	}
	if p.Parity != "" {
		c.Parity = p.Parity
	}
	if p.StopBits != "" {
		c.StopBits = p.StopBits
	}
	if p.FlowControl != "" {
		c.FlowControl = p.FlowControl
	}
	return c
}

// hasIdentity reports whether p carries any USB-identity field at all
// (as opposed to being pinned to a bare Path, which is a weaker, purely
// positional match).
func (p Profile) hasIdentity() bool {
	return p.VID != "" || p.PID != "" || p.SerialNumber != "" || p.Manufacturer != "" || p.Product != ""
}

// specificity scores how identity-specific a match against info would be —
// higher wins when multiple profiles or ports could match, so the most
// specific identity (VID+PID+serial number) is always preferred over a
// looser one (VID+PID alone), and matching never falls back to guessing
// among equally-specific candidates (see Resolve).
func (p Profile) specificity(info serial.PortInfo) int {
	score := 0
	if p.VID != "" && eqFold(p.VID, info.VID) {
		score += 1
	} else if p.VID != "" {
		return -1 // stated and wrong: not a match at all
	}
	if p.PID != "" && eqFold(p.PID, info.PID) {
		score += 1
	} else if p.PID != "" {
		return -1
	}
	if p.SerialNumber != "" {
		if eqFold(p.SerialNumber, info.SerialNumber) {
			score += 4
		} else {
			return -1
		}
	}
	if p.Manufacturer != "" {
		if eqFold(p.Manufacturer, info.Manufacturer) {
			score += 2
		} else {
			return -1
		}
	}
	if p.Product != "" {
		if eqFold(p.Product, info.Product) {
			score += 2
		} else {
			return -1
		}
	}
	if score == 0 {
		return -1 // profile declared no identity fields: not a real match
	}
	return score
}

func eqFold(a, b string) bool {
	return b != "" && normalizeHex(a) == normalizeHex(b)
}

// normalizeHex lowercases s and strips a leading "0x"/"0X" so VID/PID
// values compare equal regardless of how the user or the platform's
// enumerator happened to case/prefix them ("0403", "0x0403", "0X0403").
func normalizeHex(s string) string {
	s = strings.ToLower(s)
	return strings.TrimPrefix(s, "0x")
}

// Resolve finds the port that best matches p among ports (as returned by
// serial.ListDetailed). Priority:
//
//  1. An exact current Path match against an enumerated port wins outright
//     (a live path is the strongest possible signal, and this also picks up
//     fresh metadata for a device whose profile predates it).
//  2. A Path-only profile (no VID/PID/serial/manufacturer/product) whose
//     Path isn't in the enumerated list at all is trusted as an explicit
//     manual path rather than treated as "not found" — this is what makes
//     virtual/development ports (socat PTYs, USB-serial adapters the
//     platform's enumerator doesn't recognize) usable via a saved profile:
//     see ARCHITECTURE.md "Manual serial paths". internal/serial.Open never
//     consulted enumeration to begin with — it opens whatever path it's
//     given — so trusting the path here is not a special case for the
//     transport, only for how a *profile* decides what path to hand it.
//     The returned PortInfo has no USB metadata (IsUSB false, VID/PID/
//     SerialNumber/Manufacturer/Product all empty) — that is the correct,
//     honest representation of a manually-specified path, not an error.
//  3. Otherwise, the highest-specificity USB-identity match. If more than
//     one port ties at the winning specificity, Resolve returns an error
//     rather than guessing — see product spec §5, "Never randomly select
//     one device if several match."
func Resolve(p Profile, ports []serial.PortInfo) (serial.PortInfo, error) {
	if p.Path != "" {
		for _, info := range ports {
			if info.Path == p.Path {
				return info, nil
			}
		}
		if !p.hasIdentity() {
			return serial.PortInfo{Path: p.Path}, nil
		}
	}
	if !p.hasIdentity() {
		return serial.PortInfo{}, fmt.Errorf("device: profile %q has no USB identity fields and its saved path %q is not currently present", p.Alias, p.Path)
	}

	best := -1
	var winners []serial.PortInfo
	for _, info := range ports {
		s := p.specificity(info)
		if s < 0 {
			continue
		}
		switch {
		case s > best:
			best = s
			winners = []serial.PortInfo{info}
		case s == best:
			winners = append(winners, info)
		}
	}
	switch len(winners) {
	case 0:
		return serial.PortInfo{}, fmt.Errorf("device: no connected port matches profile %q", p.Alias)
	case 1:
		return winners[0], nil
	default:
		paths := make([]string, len(winners))
		for i, w := range winners {
			paths[i] = w.Path
		}
		return serial.PortInfo{}, fmt.Errorf("device: profile %q matches %d ports ambiguously (%s) — refine the profile (e.g. add a serial number) or pick one explicitly", p.Alias, len(winners), strings.Join(paths, ", "))
	}
}
