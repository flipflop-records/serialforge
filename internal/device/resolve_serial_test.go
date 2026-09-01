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
