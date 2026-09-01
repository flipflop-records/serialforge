package device

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CandidateSource distinguishes where a Virtual/Manual chooser Candidate
// came from — purely informational for the TUI's grouping/labeling; every
// source resolves to a Path that opens exactly the same way.
type CandidateSource string

const (
	CandidateSymlink      CandidateSource = "symlink"
	CandidateRecent       CandidateSource = "recent"
	CandidateSavedProfile CandidateSource = "saved_profile"
)

// Candidate is one selectable entry in the TUI's Virtual/Manual endpoint
// chooser: manual-path support must remain a fallback, not the primary
// workflow — see ARCHITECTURE.md "Virtual / manual endpoint discovery". It
// is deliberately a *different* type from serial.PortInfo: a Candidate is
// never produced by hardware auto-discovery (ListDetailed/
// enumerate_enumerator.go) and never feeds into it — mixing the two would
// be exactly the "broaden the darwin regex to every ttys*" mistake
// ARCHITECTURE.md already forbids.
type Candidate struct {
	Source CandidateSource
	// Label is the primary display text: the friendly symlink path, the
	// saved profile's alias, or the bare recent path.
	Label string
	// Path is what actually gets opened.
	Path string
	// Target is the symlink's resolved destination (e.g. "/dev/ttys004"),
	// set only for CandidateSymlink — shown as secondary metadata so the
	// user never has to interact with the resolved path directly.
	Target string
	// Available reports whether Path currently exists on disk. False
	// means "stale" (e.g. socat was stopped) — the candidate stays listed,
	// never silently dropped, so recent/saved history survives a
	// temporarily-vanished endpoint (product spec §8).
	Available bool
	// ProfileAlias is set only for CandidateSavedProfile — the device.Store
	// alias to resolve through the normal profile path (device.Resolve +
	// ResolveSerialConfig with the profile's own settings), rather than
	// connecting to Path "bare."
	ProfileAlias string
}

// FriendlySymlinkDirs are the directories DiscoverFriendlySymlinks scans by
// default. /tmp is scanned explicitly — matching every socat example in
// this project's docs — in addition to os.TempDir(), which can differ from
// /tmp depending on $TMPDIR (notably on macOS).
func FriendlySymlinkDirs() []string {
	dirs := []string{"/tmp"}
	if tmp := os.TempDir(); tmp != "" && tmp != "/tmp" {
		dirs = append(dirs, tmp)
	}
	return dirs
}

// LooksLikeSerialDevice reports whether target — a symlink's resolved
// destination — looks like a serial/pty device path. This is a
// conservative allowlist (not "everything under /dev"), so an unrelated
// /tmp symlink (a socket, a log rotation link) never shows up in the
// chooser as a fake serial candidate.
func LooksLikeSerialDevice(target string) bool {
	base := filepath.Base(target)
	lower := strings.ToLower(base)
	switch {
	case strings.HasPrefix(lower, "tty"):
		return true
	case strings.HasPrefix(lower, "cu."):
		return true
	case strings.HasPrefix(lower, "pty"):
		return true
	case strings.Contains(target, "/pts/"):
		return true
	default:
		return false
	}
}

// DiscoverFriendlySymlinks scans dirs (non-recursively, symlinks only) for
// developer-friendly endpoints — exactly the shape a `socat
// pty,link=/tmp/serialforge-a` invocation creates: a stable, readable name
// pointing at a kernel-assigned pty like /dev/ttys004. It does NOT
// enumerate every /dev/ttys*/pts/* entry directly (that would flood the
// chooser with the user's own unrelated terminal sessions — see
// ARCHITECTURE.md's "Auto-discovery must never be broadened" note);
// it only ever surfaces a symlink someone (or a tool like socat) explicitly
// created and named, whose target looks pty-like
// (LooksLikeSerialDevice). Never consulted by normal hardware
// enumeration — this is a separate discovery layer, called only from the
// TUI's Virtual/Manual chooser.
func DiscoverFriendlySymlinks(dirs []string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // unreadable/missing dir is not an error for a best-effort scan
		}
		for _, e := range entries {
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if seen[path] {
				continue
			}
			target, err := os.Readlink(path)
			if err != nil || !LooksLikeSerialDevice(target) {
				continue
			}
			seen[path] = true
			_, statErr := os.Stat(path) // follows the link; fails if the target is gone
			out = append(out, Candidate{
				Source:    CandidateSymlink,
				Label:     path,
				Path:      path,
				Target:    target,
				Available: statErr == nil,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// BuildVirtualCandidates assembles the Virtual/Manual chooser's full,
// deduplicated candidate list: live friendly symlinks first, then
// path-only saved profiles (a profile with a Path but no VID/PID/serial/
// manufacturer/product — see Profile.hasIdentity), then recent manual
// history — each source skipped for a Path already surfaced by an earlier
// (higher-priority) source, so e.g. a path that's both a live symlink and
// in recent history shows once, as the richer symlink entry.
//
// store and recent may both be nil (an empty/fresh install); symlinkDirs
// is normally FriendlySymlinkDirs() — callers pass it explicitly so tests
// can point discovery at a temp directory instead of the real /tmp.
func BuildVirtualCandidates(store *Store, recent *RecentStore, symlinkDirs []string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}

	for _, c := range DiscoverFriendlySymlinks(symlinkDirs) {
		out = append(out, c)
		seen[c.Path] = true
	}

	if store != nil {
		profiles := store.All()
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].Alias < profiles[j].Alias })
		for _, p := range profiles {
			if p.Path == "" || p.hasIdentity() || seen[p.Path] {
				continue // only path-only profiles belong here — identity-based profiles live in "Saved profiles"
			}
			seen[p.Path] = true
			_, statErr := os.Stat(p.Path)
			out = append(out, Candidate{
				Source:       CandidateSavedProfile,
				Label:        p.Alias,
				Path:         p.Path,
				Available:    statErr == nil,
				ProfileAlias: p.Alias,
			})
		}
	}

	if recent != nil {
		for _, e := range recent.All() {
			if seen[e.Path] {
				continue
			}
			seen[e.Path] = true
			_, statErr := os.Stat(e.Path)
			out = append(out, Candidate{
				Source:    CandidateRecent,
				Label:     e.Path,
				Path:      e.Path,
				Available: statErr == nil,
			})
		}
	}

	return out
}
