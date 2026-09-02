package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UIPrefs are small, purely cosmetic/behavioral TUI preferences.
type UIPrefs struct {
	Theme          string `yaml:"theme,omitempty"`        // reserved for future light/dark/etc — dark is the only theme in v0.1
	MonitorMode    string `yaml:"monitor_mode,omitempty"` // "ascii" | "hex" | "both"
	ShowTimestamps bool   `yaml:"show_timestamps"`
	LastDevice     string `yaml:"last_device,omitempty"` // alias or path, for reopening on next launch
	LastProtocol   string `yaml:"last_protocol,omitempty"`

	// MonitorSavedPacketsRatio is the user's preferred share of Monitor's
	// adjustable split given to the Saved Packets sidebar (0 < ratio < 1),
	// set by the resize keys in internal/tui/monitorsidebar.go. Zero (the
	// Go zero value, and what an app.yaml predating this feature has) means
	// "no preference recorded yet" — the TUI falls back to its own default
	// ratio; a value outside (0,1) (negative, >=1, or a malformed edit) is
	// likewise treated as absent rather than breaking layout. This is a UI
	// preference, not a Session Profile property — deliberately scoped
	// here, not to any per-device/per-protocol state, so a future Session
	// Profile never needs to own it.
	MonitorSavedPacketsRatio float64 `yaml:"monitor_saved_packets_ratio,omitempty"`
}

// ReconnectPrefs are the user-facing knobs behind session.ReconnectPolicy.
type ReconnectPrefs struct {
	Enabled        bool `yaml:"enabled"`
	InitialDelayMS int  `yaml:"initial_delay_ms"`
	MaxDelayMS     int  `yaml:"max_delay_ms"`
}

// SerialPrefs are the application-wide serial-line defaults — tier 3 of the
// four-tier setting precedence (explicit override > saved device profile >
// this > built-in default; see ARCHITECTURE.md "Serial setting precedence" and
// internal/device.ResolveSerialConfig, which is what actually applies this
// struct). Fields are plain strings, not internal/serial's typed
// Parity/StopBits/FlowControl, so this package stays a leaf with no
// dependency on internal/serial — internal/device (which already depends on
// both) does the conversion. Empty/zero fields fall through to the next
// tier, exactly like Profile's fields.
type SerialPrefs struct {
	Baud        int    `yaml:"baud,omitempty"`
	DataBits    int    `yaml:"data_bits,omitempty"`
	Parity      string `yaml:"parity,omitempty"`
	StopBits    string `yaml:"stop_bits,omitempty"`
	FlowControl string `yaml:"flow_control,omitempty"`
}

// App is the top-level app.yaml: everything that isn't a device profile, a
// protocol profile, or a custom CRC definition (those live in their own
// files under the same config directory — see internal/device,
// internal/protocol).
type App struct {
	UI        UIPrefs        `yaml:"ui"`
	Reconnect ReconnectPrefs `yaml:"reconnect"`
	Serial    SerialPrefs    `yaml:"serial"`
}

// DefaultApp returns sane defaults for a fresh install. Serial is left
// entirely zero-valued on purpose — an empty SerialPrefs means "no
// application-level override," letting internal/serial.DefaultConfig()'s
// built-in defaults (115200 8N1 none) show through untouched, per product
// spec §5's precedence rule.
func DefaultApp() App {
	return App{
		UI: UIPrefs{MonitorMode: "both", ShowTimestamps: true},
		Reconnect: ReconnectPrefs{
			Enabled:        true,
			InitialDelayMS: 500,
			MaxDelayMS:     10000,
		},
	}
}

const appFileName = "app.yaml"

// LoadApp reads app.yaml from dir, returning DefaultApp() if it doesn't
// exist yet (a fresh config directory is not an error).
func LoadApp(dir string) (App, error) {
	path := filepath.Join(dir, appFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultApp(), nil
	}
	if err != nil {
		return App{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var a App
	if err := yaml.Unmarshal(data, &a); err != nil {
		return App{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return a, nil
}

// SaveApp atomically writes a to app.yaml in dir.
func SaveApp(dir string, a App) error {
	data, err := yaml.Marshal(a)
	if err != nil {
		return fmt.Errorf("config: marshal app config: %w", err)
	}
	path := filepath.Join(dir, appFileName)
	return WriteFileAtomic(path, data, 0o600)
}
