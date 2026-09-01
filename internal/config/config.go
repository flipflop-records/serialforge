// Package config resolves SerialForge's on-disk configuration directory and
// provides an atomic-write primitive every other persistence-owning
// package (internal/device, internal/protocol, internal/checksum's custom
// CRC store) builds on. It does not itself know about devices, protocols,
// or CRCs — see product spec §27.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// EnvOverride is the environment variable checked by Dir before
// os.UserConfigDir — set by tests and by `--config` handling in cmd/serialforge
// (which sets it for the process rather than threading an override through
// every package).
const EnvOverride = "SERIALFORGE_CONFIG_DIR"

// appDirName is the app's config-directory name appended to
// os.UserConfigDir(), cased per platform convention: macOS and Windows
// name their per-app Application Support/AppData folders after the
// product's display name (Title Case — e.g. "~/Library/Application
// Support/SerialForge"), while Linux's XDG convention is lowercase
// ("~/.config/serialforge"). An explicit --config path or EnvOverride
// bypasses this entirely.
func appDirName() string {
	switch runtime.GOOS {
	case "darwin", "windows":
		return "SerialForge"
	default:
		return "serialforge"
	}
}

// Dir returns SerialForge's configuration directory, creating it (and its
// parents) if it doesn't exist yet. override, if non-empty, is used as-is
// (this is what --config <path> resolves to); otherwise EnvOverride, then
// os.UserConfigDir()/<appDirName()>.
func Dir(override string) (string, error) {
	dir := override
	if dir == "" {
		dir = os.Getenv(EnvOverride)
	}
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("config: resolve user config dir: %w", err)
		}
		dir = filepath.Join(base, appDirName())
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: create config dir %s: %w", dir, err)
	}
	return dir, nil
}

// WriteFileAtomic writes data to path by writing to a temp file in the same
// directory and renaming over the target — the write is atomic from any
// concurrent reader's point of view (never observes a partial file), and a
// crash mid-write leaves the original file untouched. perm is applied via
// os.Chmod after write (avoids umask surprises).
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("config: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("config: chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: rename into place: %w", err)
	}
	return nil
}
