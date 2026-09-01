package device

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/vtemnyakov/serialforge/internal/config"
)

const fileName = "devices.yaml"

// file is devices.yaml's on-disk shape.
type file struct {
	Devices []Profile `yaml:"devices"`
}

// Store is the loaded set of device profiles for one config directory.
// Not safe for concurrent use — callers (CLI commands, the TUI's Devices
// screen) serialize access the same way they serialize any other config
// edit.
type Store struct {
	dir      string
	profiles []Profile
}

// Load reads devices.yaml from dir (creating an empty in-memory store, not
// a file, if it doesn't exist yet — Save is what actually creates it).
func Load(dir string) (*Store, error) {
	path := filepath.Join(dir, fileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Store{dir: dir}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("device: read %s: %w", path, err)
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("device: parse %s: %w", path, err)
	}
	return &Store{dir: dir, profiles: f.Devices}, nil
}

// Save atomically writes the store's profiles to devices.yaml.
func (s *Store) Save() error {
	data, err := yaml.Marshal(file{Devices: s.profiles})
	if err != nil {
		return fmt.Errorf("device: marshal: %w", err)
	}
	return config.WriteFileAtomic(filepath.Join(s.dir, fileName), data, 0o600)
}

// All returns every saved profile, in file order.
func (s *Store) All() []Profile { return append([]Profile(nil), s.profiles...) }

// Get returns the profile with the given alias.
func (s *Store) Get(alias string) (Profile, bool) {
	for _, p := range s.profiles {
		if p.Alias == alias {
			return p, true
		}
	}
	return Profile{}, false
}

// Put creates or replaces the profile with p.Alias.
func (s *Store) Put(p Profile) error {
	if p.Alias == "" {
		return fmt.Errorf("device: profile alias must not be empty")
	}
	for i, existing := range s.profiles {
		if existing.Alias == p.Alias {
			s.profiles[i] = p
			return nil
		}
	}
	s.profiles = append(s.profiles, p)
	return nil
}

// Delete removes the profile with the given alias. Reports whether it
// existed.
func (s *Store) Delete(alias string) bool {
	for i, p := range s.profiles {
		if p.Alias == alias {
			s.profiles = append(s.profiles[:i], s.profiles[i+1:]...)
			return true
		}
	}
	return false
}

// Rename changes a profile's alias, failing if oldAlias doesn't exist or
// newAlias is already taken.
func (s *Store) Rename(oldAlias, newAlias string) error {
	if _, exists := s.Get(newAlias); exists {
		return fmt.Errorf("device: alias %q already exists", newAlias)
	}
	for i, p := range s.profiles {
		if p.Alias == oldAlias {
			s.profiles[i].Alias = newAlias
			return nil
		}
	}
	return fmt.Errorf("device: no profile named %q", oldAlias)
}

// Clone duplicates a profile under a new alias.
func (s *Store) Clone(alias, newAlias string) error {
	p, ok := s.Get(alias)
	if !ok {
		return fmt.Errorf("device: no profile named %q", alias)
	}
	p.Alias = newAlias
	return s.Put(p)
}
