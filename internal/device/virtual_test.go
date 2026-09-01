package device

import (
	"os"
	"path/filepath"
	"testing"
)

func symlinkTo(t *testing.T, dir, name, target string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("symlink %s -> %s: %v", path, target, err)
	}
	return path
}

func TestLooksLikeSerialDevice(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		{"/dev/ttys004", true},
		{"/dev/ttyUSB0", true},
		{"/dev/cu.usbserial-1410", true},
		{"/dev/pts/3", true},
		{"/dev/ptyp3", true},
		{"/etc/passwd", false},
		{"/tmp/some-log-rotation-target", false},
		{"/dev/null", false},
	}
	for _, c := range cases {
		if got := LooksLikeSerialDevice(c.target); got != c.want {
			t.Errorf("LooksLikeSerialDevice(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}

// TestDiscoverFriendlySymlinksFindsSocatStyleLinks is the regression case
// for the exact reported workflow: `socat ... link=/tmp/serialforge-a`
// creates a symlink to a kernel pty (simulated here since a real PTY isn't
// available/needed for this unit test — see internal/serial/pty_test.go
// for the real-socat integration coverage).
func TestDiscoverFriendlySymlinksFindsSocatStyleLinks(t *testing.T) {
	dir := t.TempDir()
	a := symlinkTo(t, dir, "serialforge-a", "/dev/ttys004")
	_ = symlinkTo(t, dir, "serialforge-b", "/dev/ttys005")
	// A symlink present in the same directory that does NOT look pty-like
	// must never appear as a candidate.
	symlinkTo(t, dir, "not-a-serial-thing", "/etc/hosts")

	cands := DiscoverFriendlySymlinks([]string{dir})
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(cands), cands)
	}
	if cands[0].Path != a || cands[0].Target != "/dev/ttys004" {
		t.Errorf("cands[0] = %+v, want Path=%s Target=/dev/ttys004", cands[0], a)
	}
	if cands[0].Source != CandidateSymlink {
		t.Errorf("Source = %q, want %q", cands[0].Source, CandidateSymlink)
	}
}

func TestDiscoverFriendlySymlinksMarksStaleTarget(t *testing.T) {
	dir := t.TempDir()
	// The target ("ttys999") looks pty-like by name but doesn't actually
	// exist — simulates socat having been stopped after the symlink was
	// created (the symlink itself survives; its target does not).
	symlinkTo(t, dir, "serialforge-a", filepath.Join(dir, "ttys999"))

	cands := DiscoverFriendlySymlinks([]string{dir})
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(cands), cands)
	}
	if cands[0].Available {
		t.Error("a symlink whose target doesn't exist should be marked unavailable, not dropped")
	}
}

func TestDiscoverFriendlySymlinksEmptyDirIsFine(t *testing.T) {
	dir := t.TempDir()
	if cands := DiscoverFriendlySymlinks([]string{dir}); len(cands) != 0 {
		t.Errorf("empty dir should yield zero candidates, got %d", len(cands))
	}
	// A nonexistent directory must not error/panic — best-effort scan.
	if cands := DiscoverFriendlySymlinks([]string{filepath.Join(dir, "nope")}); len(cands) != 0 {
		t.Errorf("nonexistent dir should yield zero candidates, got %d", len(cands))
	}
}

func TestBuildVirtualCandidatesDedupesAcrossSources(t *testing.T) {
	dir := t.TempDir()
	a := symlinkTo(t, dir, "serialforge-a", "/dev/ttys004")

	configDir := t.TempDir()
	store, err := Load(configDir)
	if err != nil {
		t.Fatal(err)
	}
	// A path-only profile pointing at the SAME path as the live symlink —
	// must not produce a duplicate row.
	if err := store.Put(Profile{Alias: "virtual-uart", Path: a}); err != nil {
		t.Fatal(err)
	}
	// A distinct path-only profile.
	if err := store.Put(Profile{Alias: "dev-board", Path: "/tmp/my-uart"}); err != nil {
		t.Fatal(err)
	}
	// An identity-based profile (has VID/PID) must NOT appear here at all —
	// it belongs only in "Saved profiles."
	if err := store.Put(Profile{Alias: "fpga", VID: "0403", PID: "6010"}); err != nil {
		t.Fatal(err)
	}

	recent, err := LoadRecent(configDir)
	if err != nil {
		t.Fatal(err)
	}
	recent.Touch(a)                  // duplicate of the live symlink — must be deduped
	recent.Touch("/tmp/recent-only") // a genuinely distinct recent path

	cands := BuildVirtualCandidates(store, recent, []string{dir})

	var gotSymlink, gotSavedDevBoard, gotRecentOnly bool
	for _, c := range cands {
		switch {
		case c.Path == a && c.Source == CandidateSymlink:
			gotSymlink = true
		case c.Path == "/tmp/my-uart" && c.Source == CandidateSavedProfile && c.Label == "dev-board":
			gotSavedDevBoard = true
		case c.Path == "/tmp/recent-only" && c.Source == CandidateRecent:
			gotRecentOnly = true
		case c.ProfileAlias == "fpga":
			t.Errorf("identity-based profile %q leaked into virtual candidates: %+v", c.ProfileAlias, c)
		}
	}
	if !gotSymlink {
		t.Error("expected the live symlink candidate for the shared path")
	}
	if !gotSavedDevBoard {
		t.Error("expected the distinct path-only saved profile 'dev-board'")
	}
	if !gotRecentOnly {
		t.Error("expected the distinct recent-only path")
	}

	// Exactly one row for the shared path `a`, not two.
	count := 0
	for _, c := range cands {
		if c.Path == a {
			count++
		}
	}
	if count != 1 {
		t.Errorf("path %q appeared %d times, want exactly 1 (deduped)", a, count)
	}
}

func TestBuildVirtualCandidatesNilStoreAndRecent(t *testing.T) {
	dir := t.TempDir()
	symlinkTo(t, dir, "serialforge-a", "/dev/ttys004")
	cands := BuildVirtualCandidates(nil, nil, []string{dir})
	if len(cands) != 1 {
		t.Fatalf("got %d candidates with nil store/recent, want 1 (the symlink)", len(cands))
	}
}

func TestBuildVirtualCandidatesEmpty(t *testing.T) {
	dir := t.TempDir()
	cands := BuildVirtualCandidates(nil, nil, []string{dir})
	if len(cands) != 0 {
		t.Errorf("got %d candidates, want 0 (empty state)", len(cands))
	}
}
