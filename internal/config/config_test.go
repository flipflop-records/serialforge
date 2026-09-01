package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAppDirNameMatchesPlatformConvention(t *testing.T) {
	got := appDirName()
	want := "serialforge"
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		want = "SerialForge"
	}
	if got != want {
		t.Errorf("appDirName() on %s = %q, want %q", runtime.GOOS, got, want)
	}
}

func TestDirCreatesDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "nested", "serialforge")
	dir, err := Dir(target)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if dir != target {
		t.Errorf("Dir() = %q, want %q", dir, target)
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Fatalf("target dir not created: %v", err)
	}
}

func TestDirUsesEnvOverride(t *testing.T) {
	base := t.TempDir()
	t.Setenv(EnvOverride, base)
	dir, err := Dir("")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if dir != base {
		t.Errorf("Dir() = %q, want %q", dir, base)
	}
}

func TestWriteFileAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.yaml")
	if err := WriteFileAtomic(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
	// No stray temp files left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1 (no leftover temp files): %v", len(entries), entries)
	}
}

func TestWriteFileAtomicOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.yaml")
	if err := WriteFileAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}
}

func TestAppConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()

	loaded, err := LoadApp(dir)
	if err != nil {
		t.Fatalf("LoadApp (missing file): %v", err)
	}
	if loaded != DefaultApp() {
		t.Errorf("LoadApp on empty dir = %+v, want DefaultApp()", loaded)
	}

	loaded.UI.LastDevice = "fpga"
	loaded.Reconnect.Enabled = false
	if err := SaveApp(dir, loaded); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}

	reloaded, err := LoadApp(dir)
	if err != nil {
		t.Fatalf("LoadApp: %v", err)
	}
	if reloaded != loaded {
		t.Errorf("reloaded = %+v, want %+v", reloaded, loaded)
	}
}
