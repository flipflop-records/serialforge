package savedpacket

import (
	"strings"
	"testing"

	"github.com/vtemnyakov/serialforge/internal/checksum"
	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/protocol"
)

// demoSchema mirrors internal/packet's own demoSchema (the product spec's
// example protocol): a 14-byte packet, HEADER/COMMAND/ADDRESS/DATA/RESERVED,
// CRC-8/MAXIM-DOW tail.
func demoSchema() packet.Schema {
	return packet.Schema{
		Name:      "demo",
		TotalSize: 14,
		Fields: []packet.Field{
			{Name: "HEADER", Size: 2, Format: packet.FormatHex},
			{Name: "COMMAND", Size: 1, Format: packet.FormatUint},
			{Name: "ADDRESS", Size: 4, Endianness: packet.BigEndian, Format: packet.FormatHex},
			{Name: "DATA", Size: 4, Endianness: packet.BigEndian, Format: packet.FormatHex},
			{Name: "RESERVED", Size: 2, Format: packet.FormatRaw},
		},
		Checksum: checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/MAXIM-DOW"},
	}
}

func newStoreWithSchema(t *testing.T, dir string, sc packet.Schema) *protocol.Store {
	t.Helper()
	store, err := protocol.Load(dir)
	if err != nil {
		t.Fatalf("protocol.Load: %v", err)
	}
	if err := store.Put(sc); err != nil {
		t.Fatalf("protocol.Put: %v", err)
	}
	return store
}

func validValues() map[string]string {
	return map[string]string{
		"HEADER":   "AA55",
		"COMMAND":  "02",
		"ADDRESS":  "00C017FF",
		"DATA":     "00000001",
		"RESERVED": "0000",
	}
}

func TestBuildMatchesPacketBuildExactly(t *testing.T) {
	dir := t.TempDir()
	sc := demoSchema()
	protocols := newStoreWithSchema(t, dir, sc)

	sp := SavedPacket{Name: "get-status", Protocol: "demo", Values: validValues(), CRCMode: CRCModeAuto}
	got, err := sp.Build(protocols)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	values := packet.Values{
		"HEADER":   {0xAA, 0x55},
		"COMMAND":  {0x02},
		"ADDRESS":  {0x00, 0xC0, 0x17, 0xFF},
		"DATA":     {0x00, 0x00, 0x00, 0x01},
		"RESERVED": {0x00, 0x00},
	}
	want, err := packet.Build(sc, values, nil)
	if err != nil {
		t.Fatalf("packet.Build: %v", err)
	}
	if string(got.Raw) != string(want.Raw) {
		t.Errorf("Build().Raw = % X, want % X", got.Raw, want.Raw)
	}
}

func TestAutoCRCRecalculatesAfterFieldChange(t *testing.T) {
	dir := t.TempDir()
	sc := demoSchema()
	protocols := newStoreWithSchema(t, dir, sc)

	sp := SavedPacket{Name: "get-status", Protocol: "demo", Values: validValues(), CRCMode: CRCModeAuto}
	first, err := sp.Build(protocols)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Same SavedPacket, but the protocol's schema changed underneath it
	// (different CRC algorithm, same 1-byte width so the schema stays
	// valid without also resizing fields) — AUTO must recompute against
	// the CURRENT schema, never a value cached at save time.
	sc2 := sc
	sc2.Checksum = checksum.Definition{Mode: checksum.ModePreset, Preset: "CRC-8/SAE-J1850"}
	if err := protocols.Put(sc2); err != nil {
		t.Fatalf("protocol.Put: %v", err)
	}
	second, err := sp.Build(protocols)
	if err != nil {
		t.Fatalf("Build after protocol change: %v", err)
	}
	if string(second.Raw[:len(second.Raw)-1]) != string(first.Raw[:len(first.Raw)-1]) {
		t.Errorf("field bytes changed unexpectedly: %X vs %X", second.Raw, first.Raw)
	}
	if second.Raw[len(second.Raw)-1] == first.Raw[len(first.Raw)-1] {
		t.Errorf("expected AUTO CRC byte to change after the protocol's CRC algorithm changed, got identical trailing byte 0x%X in both", second.Raw[len(second.Raw)-1])
	}
}

func TestOverrideCRCPreservedExactly(t *testing.T) {
	dir := t.TempDir()
	sc := demoSchema()
	protocols := newStoreWithSchema(t, dir, sc)

	sp := SavedPacket{
		Name: "fault-injected", Protocol: "demo", Values: validValues(),
		CRCMode: CRCModeOverride, CRCOverride: "42",
	}
	pkt, err := sp.Build(protocols)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !pkt.CRC.Manual {
		t.Error("CRC.Manual = false, want true for an override")
	}
	if pkt.CRC.Received != 0x42 {
		t.Errorf("CRC.Received = 0x%X, want 0x42", pkt.CRC.Received)
	}
	if pkt.Raw[len(pkt.Raw)-1] != 0x42 {
		t.Errorf("last raw byte = 0x%X, want 0x42 (the override, not AUTO)", pkt.Raw[len(pkt.Raw)-1])
	}
}

func TestResolveProtocolMissing(t *testing.T) {
	dir := t.TempDir()
	protocols, _ := protocol.Load(dir)
	sp := SavedPacket{Name: "x", Protocol: "does-not-exist", Values: validValues()}
	res := sp.Resolve(protocols)
	if res.Status != StatusProtocolMissing {
		t.Errorf("Status = %q, want %q", res.Status, StatusProtocolMissing)
	}
	if _, err := sp.Build(protocols); err == nil {
		t.Error("Build() = nil error, want error for missing protocol")
	}
}

// TestResolveProtocolInvalidDuplicateFieldNames pins down the field-identity
// invariant this package's Values map depends on: packet.Schema.Validate
// already rejects duplicate field names, but protocol.Store.Put accepts an
// invalid draft (the Designer needs to persist in-progress work). A
// SavedPacket referencing such a draft must report a clean, diagnosable
// StatusProtocolInvalid — never silently collapse two same-named fields'
// values through the map.
func TestResolveProtocolInvalidDuplicateFieldNames(t *testing.T) {
	dir := t.TempDir()
	protocols, _ := protocol.Load(dir)
	bad := packet.Schema{
		Name:      "broken",
		TotalSize: 2,
		Fields: []packet.Field{
			{Name: "X", Size: 1},
			{Name: "X", Size: 1}, // duplicate name — invalid, but Store.Put doesn't reject it
		},
	}
	if err := protocols.Put(bad); err != nil {
		t.Fatalf("protocol.Put (drafts must be accepted): %v", err)
	}
	if bad.Validate() == nil {
		t.Fatal("test setup: expected duplicate-name schema to fail Validate")
	}

	sp := SavedPacket{Name: "x", Protocol: "broken", Values: map[string]string{"X": "01"}}
	res := sp.Resolve(protocols)
	if res.Status != StatusProtocolInvalid {
		t.Fatalf("Status = %q, want %q", res.Status, StatusProtocolInvalid)
	}
	if res.Err() == nil {
		t.Error("Resolve().Err() = nil, want the underlying Validate() error")
	}
	if _, err := sp.Build(protocols); err == nil {
		t.Error("Build() = nil error, want error for an invalid protocol schema")
	}
}

func TestResolveFieldProblems(t *testing.T) {
	dir := t.TempDir()
	sc := demoSchema()
	protocols := newStoreWithSchema(t, dir, sc)

	t.Run("missing_value", func(t *testing.T) {
		vals := validValues()
		delete(vals, "DATA")
		sp := SavedPacket{Name: "x", Protocol: "demo", Values: vals}
		res := sp.Resolve(protocols)
		assertProblemKind(t, res, "DATA", ProblemMissingValue)
	})

	t.Run("unknown_field", func(t *testing.T) {
		vals := validValues()
		vals["EXTRA"] = "00"
		sp := SavedPacket{Name: "x", Protocol: "demo", Values: vals}
		res := sp.Resolve(protocols)
		assertProblemKind(t, res, "EXTRA", ProblemUnknownField)
	})

	t.Run("size_mismatch", func(t *testing.T) {
		vals := validValues()
		vals["COMMAND"] = "0002" // 2 bytes, field is 1
		sp := SavedPacket{Name: "x", Protocol: "demo", Values: vals}
		res := sp.Resolve(protocols)
		assertProblemKind(t, res, "COMMAND", ProblemSizeMismatch)
	})
}

func assertProblemKind(t *testing.T, res Resolution, field string, kind ProblemKind) {
	t.Helper()
	if res.Status != StatusIncompatible {
		t.Fatalf("Status = %q, want %q", res.Status, StatusIncompatible)
	}
	for _, p := range res.Problems {
		if p.Field == field && p.Kind == kind {
			return
		}
	}
	t.Errorf("no problem %s/%s found in %+v", field, kind, res.Problems)
}

func TestResolvePicksUpProtocolEditsNotACachedCopy(t *testing.T) {
	dir := t.TempDir()
	sc := demoSchema()
	protocols := newStoreWithSchema(t, dir, sc)
	sp := SavedPacket{Name: "x", Protocol: "demo", Values: validValues(), CRCMode: CRCModeAuto}

	if res := sp.Resolve(protocols); res.Status != StatusOK {
		t.Fatalf("initial Status = %q, want ok", res.Status)
	}

	// Rename a field in the protocol after the SavedPacket was created —
	// Resolve must reflect this immediately (no embedded schema copy).
	sc2 := sc
	sc2.Fields = append([]packet.Field(nil), sc.Fields...)
	sc2.Fields[1] = packet.Field{Name: "OPCODE", Size: 1, Format: packet.FormatUint} // was COMMAND
	if err := protocols.Put(sc2); err != nil {
		t.Fatalf("protocol.Put: %v", err)
	}

	res := sp.Resolve(protocols)
	if res.Status != StatusIncompatible {
		t.Fatalf("Status after protocol field rename = %q, want incompatible", res.Status)
	}
	var sawMissingOpcode, sawUnknownCommand bool
	for _, p := range res.Problems {
		if p.Field == "OPCODE" && p.Kind == ProblemMissingValue {
			sawMissingOpcode = true
		}
		if p.Field == "COMMAND" && p.Kind == ProblemUnknownField {
			sawUnknownCommand = true
		}
	}
	if !sawMissingOpcode || !sawUnknownCommand {
		t.Errorf("expected OPCODE missing + COMMAND stale, got %+v", res.Problems)
	}
}

func TestFieldProblemStringIsHumanReadable(t *testing.T) {
	cases := []struct {
		p    FieldProblem
		want string
	}{
		{FieldProblem{Field: "flags", Kind: ProblemMissingValue}, "missing field: flags"},
		{FieldProblem{Field: "old", Kind: ProblemUnknownField}, "stale field (no longer in protocol): old"},
		{FieldProblem{Field: "addr", Kind: ProblemSizeMismatch}, "value no longer fits: addr"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

func TestBuildBadCRCOverrideHex(t *testing.T) {
	dir := t.TempDir()
	sc := demoSchema()
	protocols := newStoreWithSchema(t, dir, sc)
	sp := SavedPacket{Name: "x", Protocol: "demo", Values: validValues(), CRCMode: CRCModeOverride, CRCOverride: "ZZ"}
	_, err := sp.Build(protocols)
	if err == nil || !strings.Contains(err.Error(), "CRC override") {
		t.Errorf("Build() err = %v, want a CRC-override error", err)
	}
}
