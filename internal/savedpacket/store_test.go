package savedpacket

import "testing"

func sample(name, hotkey string) SavedPacket {
	return SavedPacket{
		Name:     name,
		Protocol: "demo",
		Values:   map[string]string{"COMMAND": "02"},
		CRCMode:  CRCModeAuto,
		Hotkey:   hotkey,
	}
}

func TestStorePersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("fresh store All() = %v, want empty", s.All())
	}

	sp := SavedPacket{
		Name: "get-status", Protocol: "control-v1",
		Values:  map[string]string{"command": "02", "address": "00000000", "data": "0000000000000000"},
		CRCMode: CRCModeAuto,
		Hotkey:  "'",
	}
	if err := s.Put(sp); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	got, ok := s2.Get("get-status")
	if !ok {
		t.Fatal("saved packet did not survive reload")
	}
	if got.Protocol != "control-v1" || got.Hotkey != "'" || got.Values["address"] != "00000000" {
		t.Errorf("reloaded packet = %+v, want protocol/hotkey/values preserved", got)
	}
}

func TestStoreLoadMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(s.All()) != 0 {
		t.Errorf("All() = %v, want empty", s.All())
	}
}

func TestStorePutCreatesOrReplacesPreservingOrder(t *testing.T) {
	s := &Store{}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Put(sample("a", "")))
	must(s.Put(sample("b", "")))
	must(s.Put(sample("c", "")))
	if got := s.Names(); got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("Names() = %v, want [a b c]", got)
	}
	// Replacing "b" must not move it to the end.
	replaced := sample("b", "x")
	must(s.Put(replaced))
	if got := s.Names(); got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("Names() after replace = %v, want [a b c] (order preserved)", got)
	}
	b, _ := s.Get("b")
	if b.Hotkey != "x" {
		t.Errorf("Get(b).Hotkey = %q, want x (replace, not append)", b.Hotkey)
	}
}

func TestStoreDeleteRenameDuplicate(t *testing.T) {
	s := &Store{}
	_ = s.Put(sample("get-status", "'"))
	_ = s.Put(sample("reset", "."))

	if !s.Delete("reset") {
		t.Fatal("Delete(reset) = false, want true")
	}
	if s.Delete("reset") {
		t.Fatal("Delete(reset) again = true, want false (already gone)")
	}
	if len(s.All()) != 1 {
		t.Fatalf("All() after delete = %v, want 1 entry", s.All())
	}

	if err := s.Rename("get-status", "status"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, ok := s.Get("get-status"); ok {
		t.Error("old name still resolves after Rename")
	}
	renamed, ok := s.Get("status")
	if !ok || renamed.Hotkey != "'" {
		t.Errorf("Rename lost data: %+v", renamed)
	}

	if err := s.Duplicate("status", "status-copy"); err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	dup, ok := s.Get("status-copy")
	if !ok {
		t.Fatal("Duplicate did not create the new entry")
	}
	if dup.Hotkey != "" {
		t.Errorf("Duplicate copied the hotkey (%q) — a duplicate must start unbound", dup.Hotkey)
	}
	if dup.Values["COMMAND"] != "02" {
		t.Errorf("Duplicate did not copy field values: %+v", dup.Values)
	}
	// Mutating the duplicate's Values must not affect the original — Put's
	// storage is a real copy, not a shared map.
	dup.Values["COMMAND"] = "FF"
	orig, _ := s.Get("status")
	if orig.Values["COMMAND"] != "02" {
		t.Errorf("mutating duplicate leaked into original: %+v", orig.Values)
	}
}

func TestStoreRenameConflict(t *testing.T) {
	s := &Store{}
	_ = s.Put(sample("a", ""))
	_ = s.Put(sample("b", ""))
	if err := s.Rename("a", "b"); err == nil {
		t.Error("Rename onto an existing name = nil error, want conflict error")
	}
}

func TestStoreHotkeyLookupAndConflict(t *testing.T) {
	s := &Store{}
	_ = s.Put(sample("get-status", "'"))
	_ = s.Put(sample("reset", "."))

	found, ok := s.FindByHotkey("'")
	if !ok || found.Name != "get-status" {
		t.Errorf("FindByHotkey(') = %+v, %v, want get-status", found, ok)
	}
	if _, ok := s.FindByHotkey("z"); ok {
		t.Error("FindByHotkey(unbound key) = true, want false")
	}
	if _, ok := s.FindByHotkey(""); ok {
		t.Error("FindByHotkey(empty) = true, want false")
	}

	if name, ok := s.HotkeyConflict(".", "get-status"); !ok || name != "reset" {
		t.Errorf("HotkeyConflict(., get-status) = %q, %v, want reset/true", name, ok)
	}
	// Re-assigning a packet's own current hotkey to itself is not a conflict.
	if _, ok := s.HotkeyConflict("'", "get-status"); ok {
		t.Error("HotkeyConflict against the packet's own current hotkey = true, want false")
	}
	if _, ok := s.HotkeyConflict("z", "get-status"); ok {
		t.Error("HotkeyConflict(unused key) = true, want false")
	}
}

func TestStorePutEmptyNameRejected(t *testing.T) {
	s := &Store{}
	if err := s.Put(SavedPacket{}); err == nil {
		t.Error("Put with empty name = nil error, want error")
	}
}
