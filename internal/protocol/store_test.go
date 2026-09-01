package protocol

import (
	"os"
	"testing"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
)

func exampleSchema() packet.Schema {
	return packet.Schema{
		Name:      "demo",
		TotalSize: 14,
		Fields: []packet.Field{
			{Name: "header", Size: 2, Format: packet.FormatHex},
			{Name: "command", Size: 1, Format: packet.FormatUint},
			{Name: "address", Size: 4, Endianness: packet.BigEndian, Format: packet.FormatHex},
			{Name: "value", Size: 4, Endianness: packet.BigEndian, Format: packet.FormatHex},
			{Name: "reserved", Size: 2, Format: packet.FormatRaw},
		},
		Checksum: checksum.Definition{
			Mode:   checksum.ModePreset,
			Preset: "CRC-8/MAXIM-DOW",
		},
	}
}

func TestStoreRoundTripsSchemaThroughYAML(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sc := exampleSchema()
	if err := sc.Validate(); err != nil {
		t.Fatalf("example schema should validate: %v", err)
	}
	if err := s.Put(sc); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load (reload): %v", err)
	}
	got, ok := reloaded.Get("demo")
	if !ok {
		t.Fatal("reloaded store missing the saved schema")
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("reloaded schema should still validate: %v", err)
	}
	if got.TotalSize != 14 || len(got.Fields) != 5 || got.Checksum.Preset != "CRC-8/MAXIM-DOW" {
		t.Fatalf("reloaded schema = %+v", got)
	}
}

func TestStorePutAllowsInvalidDraft(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	draft := packet.Schema{Name: "wip", TotalSize: 32} // no fields yet, deliberately incomplete
	if err := draft.Validate(); err == nil {
		t.Fatal("test setup: expected the empty-field draft to be invalid")
	}
	if err := s.Put(draft); err != nil {
		t.Fatalf("Put should accept an in-progress draft: %v", err)
	}
	if _, ok := s.Get("wip"); !ok {
		t.Fatal("draft was not stored")
	}
}

func TestStoreCloneRenameDelete(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	s.Put(exampleSchema())

	if err := s.Clone("demo", "demo-v2"); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	clone, ok := s.Get("demo-v2")
	if !ok || clone.Name != "demo-v2" {
		t.Fatalf("clone = %+v, ok=%v", clone, ok)
	}
	clone.Fields[0].Name = "CHANGED"
	original, _ := s.Get("demo")
	if original.Fields[0].Name == "CHANGED" {
		t.Fatal("Clone shares field storage with the original")
	}

	if err := s.Rename("demo-v2", "demo-final"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, ok := s.Get("demo-v2"); ok {
		t.Error("old name still resolves after Rename")
	}

	if !s.Delete("demo-final") {
		t.Error("Delete returned false for an existing profile")
	}
	if names := s.Names(); len(names) != 1 || names[0] != "demo" {
		t.Errorf("Names() = %v, want [demo]", names)
	}
}

// TestTrackedExampleLoadsAndValidates guards against the checked-in
// examples/protocols/uart-demo.yaml silently drifting from what the Store
// actually parses — if this test breaks, the example file needs updating,
// not the other way around.
func TestTrackedExampleLoadsAndValidates(t *testing.T) {
	data, err := os.ReadFile("../../examples/protocols/uart-demo.yaml")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sc, err := s.Import(data)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("tracked example does not validate: %v", err)
	}
	if sc.Name != "uart-demo" || sc.TotalSize != 14 {
		t.Fatalf("unexpected schema from tracked example: %+v", sc)
	}
}

func TestExportImport(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	s.Put(exampleSchema())

	data, err := s.Export("demo")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	dir2 := t.TempDir()
	s2, _ := Load(dir2)
	imported, err := s2.Import(data)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imported.Name != "demo" || imported.TotalSize != 14 {
		t.Fatalf("imported = %+v", imported)
	}
	if _, ok := s2.Get("demo"); !ok {
		t.Fatal("Import did not store the profile")
	}
}
