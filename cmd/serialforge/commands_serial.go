package main

import (
	"encoding/hex"
	"fmt"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/device"
	"github.com/vtemnyakov/serialforge/internal/framing"
	"github.com/vtemnyakov/serialforge/internal/serial"
	"github.com/vtemnyakov/serialforge/internal/session"
)

// resolveDevice figures out which OS port path and serial.Config to use for
// deviceArg: first tries it as a saved device.Profile alias (resolved
// against currently connected ports via device.Resolve, so a renumbered
// /dev/cu.* or COM path still works, and so a manual/virtual path with no
// USB identity is trusted as-is — product spec §5), and falls back to
// treating deviceArg as a literal OS path directly (e.g. "/dev/ttyUSB0",
// "COM3", a socat PTY link) for callers with no saved profile.
//
// The returned Config follows the product's serial-setting precedence
// (device.ResolveSerialConfig — explicit override > profile > app-config
// default > built-in default) even when deviceArg isn't a saved profile:
// the app-config and built-in tiers still apply, which is what makes
// `serialforge monitor --port /tmp/serialforge-a` work with no --baud and
// no saved profile at all.
func resolveDevice(g globalFlags, deviceArg string, overrideBaud *int) (path string, cfg serial.Config, err error) {
	dir, err := config.Dir(g.configPath)
	if err != nil {
		return "", cfg, err
	}
	appCfg, err := config.LoadApp(dir)
	if err != nil {
		return "", cfg, err
	}
	store, err := device.Load(dir)
	if err != nil {
		return "", cfg, err
	}

	var profile *device.Profile
	if p, ok := store.Get(deviceArg); ok {
		profile = &p
		ports, err := serial.ListDetailed()
		if err != nil {
			return "", cfg, fmt.Errorf("list ports to resolve device %q: %w", deviceArg, err)
		}
		info, err := device.Resolve(p, ports)
		if err != nil {
			return "", cfg, err
		}
		path = info.Path
	} else {
		path = deviceArg
	}

	cfg = device.ResolveSerialConfig(appCfg, profile, overrideBaud)
	return path, cfg, nil
}

// openSession opens path with cfg and wraps it in a session.Session using
// framer, starting its RX loop. Reconnect is disabled here deliberately —
// CLI commands are short-lived and a broken connection should surface as an
// error immediately rather than retry silently in the background.
func openSession(path string, cfg serial.Config, framer framing.Framer) (*session.Session, error) {
	port, err := serial.Open(path, cfg)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	sess := session.New(session.Config{Port: port, Framer: framer})
	return sess, nil
}

func parseHexArg(s string) ([]byte, error) {
	s = cleanHex(s)
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(s)
}
