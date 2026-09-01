// Package protocol persists packet.Schema values as named, reusable
// "protocol profiles" (product spec §18) — create/edit/clone/rename/
// delete/import/export. It is pure persistence: the schema itself, its
// validation, serialization, and decoding all stay in internal/packet —
// this package never reimplements or wraps that logic, only stores it.
package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/vtemnyakov/serialforge/internal/config"
	"github.com/vtemnyakov/serialforge/internal/packet"
)

const fileName = "protocols.yaml"

// file is protocols.yaml's on-disk shape: a name-keyed map, matching the
// product spec's illustrative "protocols: <name>: ..." example (§18).
type file struct {
	Protocols map[string]packet.Schema `yaml:"protocols"`
}

// Store is the loaded set of protocol profiles for one config directory.
// Not safe for concurrent use, same as internal/device.Store.
type Store struct {
	dir       string
	protocols map[string]packet.Schema
}

// Load reads protocols.yaml from dir, returning an empty store (not an
// error) if the file doesn't exist yet.
func Load(dir string) (*Store, error) {
	path := filepath.Join(dir, fileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Store{dir: dir, protocols: map[string]packet.Schema{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("protocol: read %s: %w", path, err)
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("protocol: parse %s: %w", path, err)
	}
	if f.Protocols == nil {
		f.Protocols = map[string]packet.Schema{}
	}
	return &Store{dir: dir, protocols: f.Protocols}, nil
}

// Save atomically writes every profile to protocols.yaml.
func (s *Store) Save() error {
	data, err := yaml.Marshal(file{Protocols: s.protocols})
	if err != nil {
		return fmt.Errorf("protocol: marshal: %w", err)
	}
	return config.WriteFileAtomic(filepath.Join(s.dir, fileName), data, 0o600)
}

// Names returns every saved profile's name, sorted.
func (s *Store) Names() []string {
	names := make([]string, 0, len(s.protocols))
	for name := range s.protocols {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All returns every saved schema, sorted by name.
func (s *Store) All() []packet.Schema {
	names := s.Names()
	out := make([]packet.Schema, len(names))
	for i, n := range names {
		out[i] = s.protocols[n]
	}
	return out
}

// Get returns the named profile.
func (s *Store) Get(name string) (packet.Schema, bool) {
	sc, ok := s.protocols[name]
	return sc, ok
}

// Put creates or replaces a profile under sc.Name. Saving a schema that
// does not yet Validate() is allowed — the designer needs to persist
// in-progress drafts; anything that actually serializes/decodes/sends a
// packet (TX builder, RX decode, batch) must call Schema.Validate() itself
// before using it, same as any other packet.Schema consumer.
func (s *Store) Put(sc packet.Schema) error {
	if sc.Name == "" {
		return fmt.Errorf("protocol: schema name must not be empty")
	}
	s.protocols[sc.Name] = sc
	return nil
}

// Delete removes a profile. Reports whether it existed.
func (s *Store) Delete(name string) bool {
	if _, ok := s.protocols[name]; !ok {
		return false
	}
	delete(s.protocols, name)
	return true
}

// Rename moves a profile to a new name, failing if newName is taken.
func (s *Store) Rename(oldName, newName string) error {
	sc, ok := s.protocols[oldName]
	if !ok {
		return fmt.Errorf("protocol: no profile named %q", oldName)
	}
	if _, taken := s.protocols[newName]; taken {
		return fmt.Errorf("protocol: profile %q already exists", newName)
	}
	sc.Name = newName
	delete(s.protocols, oldName)
	s.protocols[newName] = sc
	return nil
}

// Clone duplicates a profile under a new name.
func (s *Store) Clone(name, newName string) error {
	sc, ok := s.protocols[name]
	if !ok {
		return fmt.Errorf("protocol: no profile named %q", name)
	}
	sc = sc.Clone()
	sc.Name = newName
	return s.Put(sc)
}

// Export marshals a single profile to YAML, e.g. for `serialforge protocol
// export <name> > foo.yaml`.
func (s *Store) Export(name string) ([]byte, error) {
	sc, ok := s.protocols[name]
	if !ok {
		return nil, fmt.Errorf("protocol: no profile named %q", name)
	}
	return yaml.Marshal(sc)
}

// Import parses a single-schema YAML document (as produced by Export) and
// stores it. Returns the parsed schema so the caller can report what was
// imported.
func (s *Store) Import(data []byte) (packet.Schema, error) {
	var sc packet.Schema
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return packet.Schema{}, fmt.Errorf("protocol: parse imported schema: %w", err)
	}
	if err := s.Put(sc); err != nil {
		return packet.Schema{}, err
	}
	return sc, nil
}
