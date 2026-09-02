package device

import (
	"testing"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/serial"
)

// TestResolveSerialConfigPrecedence pins the exact four-tier order (product
// spec §5): explicit override > saved profile > app-config default >
// built-in default. Each subtest adds one more tier on top of the
// previous, confirming the newly-added, higher-priority tier wins while
// fields the higher tier doesn't touch still fall through untouched.
func TestResolveSerialConfigPrecedence(t *testing.T) {
	t.Run("built-in default alone", func(t *testing.T) {
		cfg := ResolveSerialConfig(config.App{}, nil, nil)
		want := serial.DefaultConfig()
		if cfg != want {
			t.Fatalf("got %+v, want built-in default %+v", cfg, want)
		}
	})

	t.Run("app config overrides built-in", func(t *testing.T) {
		appCfg := config.App{Serial: config.SerialPrefs{Baud: 57600}}
		cfg := ResolveSerialConfig(appCfg, nil, nil)
		if cfg.Baud != 57600 {
			t.Fatalf("Baud = %d, want 57600 (app config tier)", cfg.Baud)
		}
		if cfg.DataBits != serial.DefaultConfig().DataBits {
			t.Fatalf("DataBits should still be the built-in default, got %d", cfg.DataBits)
		}
	})

	t.Run("profile overrides app config", func(t *testing.T) {
		appCfg := config.App{Serial: config.SerialPrefs{Baud: 57600}}
		profile := Profile{Alias: "fpga", Baud: 230400}
		cfg := ResolveSerialConfig(appCfg, &profile, nil)
		if cfg.Baud != 230400 {
			t.Fatalf("Baud = %d, want 230400 (profile tier beats app config)", cfg.Baud)
		}
	})

	t.Run("explicit override beats everything", func(t *testing.T) {
		appCfg := config.App{Serial: config.SerialPrefs{Baud: 57600}}
		profile := Profile{Alias: "fpga", Baud: 230400}
		override := 921600
		cfg := ResolveSerialConfig(appCfg, &profile, &override)
		if cfg.Baud != 921600 {
			t.Fatalf("Baud = %d, want 921600 (explicit override beats profile and app config)", cfg.Baud)
		}
	})

	// The remaining subtests exercise the same profile > app-config >
	// built-in chain for DataBits/StopBits/FlowControl — Baud is the only
	// field with an explicit-override input today (see ResolveSerialConfig's
	// doc comment), and Parity is already covered above.
	t.Run("data bits: profile beats app config beats built-in", func(t *testing.T) {
		if cfg := ResolveSerialConfig(config.App{}, nil, nil); cfg.DataBits != serial.DefaultConfig().DataBits {
			t.Fatalf("built-in DataBits = %d, want %d", cfg.DataBits, serial.DefaultConfig().DataBits)
		}
		appCfg := config.App{Serial: config.SerialPrefs{DataBits: 7}}
		if cfg := ResolveSerialConfig(appCfg, nil, nil); cfg.DataBits != 7 {
			t.Fatalf("DataBits = %d, want 7 (app config tier)", cfg.DataBits)
		}
		profile := Profile{Alias: "fpga", DataBits: 5}
		if cfg := ResolveSerialConfig(appCfg, &profile, nil); cfg.DataBits != 5 {
			t.Fatalf("DataBits = %d, want 5 (profile tier beats app config)", cfg.DataBits)
		}
	})

	t.Run("stop bits: profile beats app config beats built-in", func(t *testing.T) {
		if cfg := ResolveSerialConfig(config.App{}, nil, nil); cfg.StopBits != serial.DefaultConfig().StopBits {
			t.Fatalf("built-in StopBits = %q, want %q", cfg.StopBits, serial.DefaultConfig().StopBits)
		}
		appCfg := config.App{Serial: config.SerialPrefs{StopBits: string(serial.StopBits2)}}
		if cfg := ResolveSerialConfig(appCfg, nil, nil); cfg.StopBits != serial.StopBits2 {
			t.Fatalf("StopBits = %q, want 2 (app config tier)", cfg.StopBits)
		}
		profile := Profile{Alias: "fpga", StopBits: serial.StopBits1_5}
		if cfg := ResolveSerialConfig(appCfg, &profile, nil); cfg.StopBits != serial.StopBits1_5 {
			t.Fatalf("StopBits = %q, want 1.5 (profile tier beats app config)", cfg.StopBits)
		}
	})

	t.Run("flow control: profile beats app config beats built-in", func(t *testing.T) {
		if cfg := ResolveSerialConfig(config.App{}, nil, nil); cfg.FlowControl != serial.DefaultConfig().FlowControl {
			t.Fatalf("built-in FlowControl = %q, want %q", cfg.FlowControl, serial.DefaultConfig().FlowControl)
		}
		appCfg := config.App{Serial: config.SerialPrefs{FlowControl: string(serial.FlowRTSCTS)}}
		if cfg := ResolveSerialConfig(appCfg, nil, nil); cfg.FlowControl != serial.FlowRTSCTS {
			t.Fatalf("FlowControl = %q, want rts_cts (app config tier)", cfg.FlowControl)
		}
		// FlowNone is Profile's zero value for this field (indistinguishable
		// from "profile doesn't say"), so use a different value to actually
		// demonstrate the profile tier winning.
		profile := Profile{Alias: "fpga", FlowControl: serial.FlowXonXoff}
		if cfg := ResolveSerialConfig(appCfg, &profile, nil); cfg.FlowControl != serial.FlowXonXoff {
			t.Fatalf("FlowControl = %q, want xon_xoff (profile tier beats app config)", cfg.FlowControl)
		}
	})

	t.Run("profile setting a field doesn't reset others to zero", func(t *testing.T) {
		appCfg := config.App{Serial: config.SerialPrefs{Parity: string(serial.ParityEven)}}
		profile := Profile{Alias: "fpga", Baud: 9600} // only sets Baud
		cfg := ResolveSerialConfig(appCfg, &profile, nil)
		if cfg.Baud != 9600 {
			t.Errorf("Baud = %d, want 9600 (from profile)", cfg.Baud)
		}
		if cfg.Parity != serial.ParityEven {
			t.Errorf("Parity = %q, want even (app-config tier should still show through since the profile didn't set it)", cfg.Parity)
		}
	})
}

func TestResolveSerialConfigNoOverridesMatchesManualPathWorkflow(t *testing.T) {
	// The exact scenario from the CLI UX report: `serialforge monitor
	// --port /tmp/serialforge-a --hex` with no --baud, no saved profile,
	// no app config — must resolve to 115200 8N1 (the built-in default),
	// not an error and not a zero-value Config.
	cfg := ResolveSerialConfig(config.DefaultApp(), nil, nil)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("resolved config should be valid: %v (%+v)", err, cfg)
	}
	if cfg.Baud != 115200 || cfg.FrameString() != "8N1" {
		t.Fatalf("got baud=%d frame=%s, want 115200 8N1", cfg.Baud, cfg.FrameString())
	}
}
