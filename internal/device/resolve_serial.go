package device

import (
	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// ResolveSerialConfig applies the product's serial-setting precedence
// (ARCHITECTURE.md "Serial setting precedence") uniformly for every caller —
// cmd/serialforge's monitor/send/batch commands and the TUI's manual
// connect and saved-profile connect both call this, so "what baud does
// this connection actually use" is answered identically everywhere:
//
//  1. explicit command/session override (overrideBaud, non-nil)
//  2. saved device-profile setting (profile, non-nil; a zero field on the
//     profile means "profile doesn't say," not "override with zero")
//  3. application-config default (appCfg.Serial)
//  4. built-in default (serial.DefaultConfig(): 115200 8N1 none)
//
// Each tier only overrides fields it actually sets — a profile that only
// pins Baud doesn't reset DataBits/Parity/StopBits/FlowControl to zero
// values, they keep falling through to the app-config/built-in tiers below
// it. Today only Baud has an explicit-override input (that's all any
// caller currently exposes as a flag/form field); the other fields are
// still layered through tiers 2-4 for when they do.
func ResolveSerialConfig(appCfg config.App, profile *Profile, overrideBaud *int) serial.Config {
	cfg := serial.DefaultConfig()         // tier 4
	applySerialPrefs(&cfg, appCfg.Serial) // tier 3
	if profile != nil {
		applyProfileSerial(&cfg, *profile) // tier 2
	}
	if overrideBaud != nil { // tier 1
		cfg.Baud = *overrideBaud
	}
	return cfg
}

func applySerialPrefs(cfg *serial.Config, p config.SerialPrefs) {
	if p.Baud != 0 {
		cfg.Baud = p.Baud
	}
	if p.DataBits != 0 {
		cfg.DataBits = p.DataBits
	}
	if p.Parity != "" {
		cfg.Parity = serial.Parity(p.Parity)
	}
	if p.StopBits != "" {
		cfg.StopBits = serial.StopBits(p.StopBits)
	}
	if p.FlowControl != "" {
		cfg.FlowControl = serial.FlowControl(p.FlowControl)
	}
}

func applyProfileSerial(cfg *serial.Config, p Profile) {
	if p.Baud != 0 {
		cfg.Baud = p.Baud
	}
	if p.DataBits != 0 {
		cfg.DataBits = p.DataBits
	}
	if p.Parity != "" {
		cfg.Parity = p.Parity
	}
	if p.StopBits != "" {
		cfg.StopBits = p.StopBits
	}
	if p.FlowControl != "" {
		cfg.FlowControl = p.FlowControl
	}
}
