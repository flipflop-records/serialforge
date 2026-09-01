package device

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vtemnyakov/serialforge/internal/config"
)

// RecentEndpoint is one manual/virtual path the user has successfully
// connected to before — the Virtual/Manual chooser's "Recently used"
// section, so a path never has to be retyped or promoted to a full saved
// profile just to reuse it (product spec §4).
type RecentEndpoint struct {
	Path     string    `yaml:"path"`
	LastUsed time.Time `yaml:"last_used"`
}

// maxRecentEndpoints bounds the history so it never accumulates unlimited
// stale paths — the oldest entry is evicted once a new one would exceed
// this (product spec §4/§8).
const maxRecentEndpoints = 8

const recentFileName = "recent_endpoints.yaml"

// timeNow is a var so tests can control ordering deterministically without
// sleeping — same pattern as internal/session's `now`.
var timeNow = time.Now

type recentFile struct {
	Endpoints []RecentEndpoint `yaml:"endpoints"`
}

// RecentStore is the loaded recent-endpoint history for one config
// directory. Not safe for concurrent use, same as device.Store/
// protocol.Store.
type RecentStore struct {
	dir   string
	items []RecentEndpoint // most-recently-used first
}

// LoadRecent reads recent_endpoints.yaml from dir, returning an empty
// store (not an error) if it doesn't exist yet.
func LoadRecent(dir string) (*RecentStore, error) {
	path := filepath.Join(dir, recentFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &RecentStore{dir: dir}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("device: read %s: %w", path, err)
	}
	var f recentFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("device: parse %s: %w", path, err)
	}
	return &RecentStore{dir: dir, items: f.Endpoints}, nil
}

// Save atomically writes the current history.
func (s *RecentStore) Save() error {
	data, err := yaml.Marshal(recentFile{Endpoints: s.items})
	if err != nil {
		return fmt.Errorf("device: marshal recent endpoints: %w", err)
	}
	return config.WriteFileAtomic(filepath.Join(s.dir, recentFileName), data, 0o600)
}

// All returns the history, most-recently-used first.
func (s *RecentStore) All() []RecentEndpoint {
	return append([]RecentEndpoint(nil), s.items...)
}

// Touch records path as just used: moves it to the front if already
// present (deduplicating rather than growing), or inserts it, then trims
// to maxRecentEndpoints. Does not save — callers persist explicitly (the
// TUI does this right after a successful connect).
func (s *RecentStore) Touch(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	filtered := make([]RecentEndpoint, 0, len(s.items))
	for _, e := range s.items {
		if e.Path != path {
			filtered = append(filtered, e)
		}
	}
	s.items = append([]RecentEndpoint{{Path: path, LastUsed: timeNow()}}, filtered...)
	if len(s.items) > maxRecentEndpoints {
		s.items = s.items[:maxRecentEndpoints]
	}
}

// Remove deletes path from history — the chooser's "remove from recent"
// action for an entry the user no longer wants suggested. Reports whether
// it was present. Does not save.
func (s *RecentStore) Remove(path string) bool {
	for i, e := range s.items {
		if e.Path == path {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return true
		}
	}
	return false
}
