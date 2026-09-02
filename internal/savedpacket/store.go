package savedpacket

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/vtemnyakov/serialforge/internal/config"
)

const fileName = "saved_packets.yaml"

// file is saved_packets.yaml's on-disk shape.
type file struct {
	Packets []SavedPacket `yaml:"packets"`
}

// Store is the loaded set of saved packets for one config directory.
// Slice-based, not map-based like protocol.Store — insertion/file order is
// preserved (product requirement: "at minimum retain deterministic
// ordering"), matching internal/device.Store's shape; this is also the
// order the Saved Packets screen and hotkey-help footer render in. Not safe
// for concurrent use, same as protocol.Store/device.Store.
type Store struct {
	dir     string
	packets []SavedPacket
}

// Load reads saved_packets.yaml from dir, returning an empty store (not an
// error) if the file doesn't exist yet.
func Load(dir string) (*Store, error) {
	path := filepath.Join(dir, fileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Store{dir: dir}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("savedpacket: read %s: %w", path, err)
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("savedpacket: parse %s: %w", path, err)
	}
	return &Store{dir: dir, packets: f.Packets}, nil
}

// Save atomically writes every saved packet to saved_packets.yaml.
func (s *Store) Save() error {
	data, err := yaml.Marshal(file{Packets: s.packets})
	if err != nil {
		return fmt.Errorf("savedpacket: marshal: %w", err)
	}
	return config.WriteFileAtomic(filepath.Join(s.dir, fileName), data, 0o600)
}

// All returns every saved packet, in file/insertion order.
func (s *Store) All() []SavedPacket { return append([]SavedPacket(nil), s.packets...) }

// Names returns every saved packet's name, in file/insertion order.
func (s *Store) Names() []string {
	names := make([]string, len(s.packets))
	for i, p := range s.packets {
		names[i] = p.Name
	}
	return names
}

// Get returns the saved packet with the given name.
func (s *Store) Get(name string) (SavedPacket, bool) {
	for _, p := range s.packets {
		if p.Name == name {
			return p, true
		}
	}
	return SavedPacket{}, false
}

// Put creates or replaces the saved packet with sp.Name, preserving its
// existing position on replace (a new entry is appended). Accepts a
// saved packet whose values don't (yet) validate against its protocol —
// same "store the draft, validate at point of use" invariant as
// protocol.Store.Put/internal/device.Store.Put.
func (s *Store) Put(sp SavedPacket) error {
	if sp.Name == "" {
		return fmt.Errorf("savedpacket: name must not be empty")
	}
	for i, existing := range s.packets {
		if existing.Name == sp.Name {
			s.packets[i] = sp
			return nil
		}
	}
	s.packets = append(s.packets, sp)
	return nil
}

// Delete removes the saved packet with the given name. Reports whether it existed.
func (s *Store) Delete(name string) bool {
	for i, p := range s.packets {
		if p.Name == name {
			s.packets = append(s.packets[:i], s.packets[i+1:]...)
			return true
		}
	}
	return false
}

// Rename changes a saved packet's name in place (preserving its position),
// failing if oldName doesn't exist or newName is already taken.
func (s *Store) Rename(oldName, newName string) error {
	if _, exists := s.Get(newName); exists {
		return fmt.Errorf("savedpacket: %q already exists", newName)
	}
	for i, p := range s.packets {
		if p.Name == oldName {
			s.packets[i].Name = newName
			return nil
		}
	}
	return fmt.Errorf("savedpacket: no saved packet named %q", oldName)
}

// Duplicate copies a saved packet under a new name, right after the
// original — values/CRC mode/protocol reference are copied; the hotkey is
// deliberately NOT copied (two saved packets can never share a hotkey, so a
// duplicate always starts unbound — see HotkeyConflict).
func (s *Store) Duplicate(name, newName string) error {
	idx := -1
	var src SavedPacket
	for i, p := range s.packets {
		if p.Name == name {
			idx, src = i, p
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("savedpacket: no saved packet named %q", name)
	}
	if _, exists := s.Get(newName); exists {
		return fmt.Errorf("savedpacket: %q already exists", newName)
	}
	dup := src
	dup.Name = newName
	dup.Hotkey = ""
	dup.Values = make(map[string]string, len(src.Values))
	for k, v := range src.Values {
		dup.Values[k] = v
	}
	s.packets = append(s.packets[:idx+1], append([]SavedPacket{dup}, s.packets[idx+1:]...)...)
	return nil
}

// FindByHotkey returns the saved packet currently bound to key, if any.
func (s *Store) FindByHotkey(key string) (SavedPacket, bool) {
	if key == "" {
		return SavedPacket{}, false
	}
	for _, p := range s.packets {
		if p.Hotkey == key {
			return p, true
		}
	}
	return SavedPacket{}, false
}

// HotkeyConflict reports the name of another saved packet already bound to
// key, excluding excludeName (the packet being assigned, so re-assigning a
// packet's own current hotkey to itself is never a conflict).
func (s *Store) HotkeyConflict(key, excludeName string) (conflictName string, ok bool) {
	if key == "" {
		return "", false
	}
	for _, p := range s.packets {
		if p.Hotkey == key && p.Name != excludeName {
			return p.Name, true
		}
	}
	return "", false
}
