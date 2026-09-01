package checksum

import (
	"reflect"
	"testing"
)

// TestAlgorithmLabelsPreset pins down the exact candidate order the TX
// Builder, RX Inspector, register diagram, and the designer's field-list
// summary all rely on for naming: canonical name first, catalogued aliases
// after, in the order Presets declares them — never re-sorted or filtered,
// so a caller picking "the longest one that fits" sees every real name for
// the algorithm.
func TestAlgorithmLabelsPreset(t *testing.T) {
	def := Definition{Mode: ModePreset, Preset: "CRC-8/MAXIM-DOW"}
	got := def.AlgorithmLabels()
	want := []string{"CRC-8/MAXIM-DOW", "CRC-8/MAXIM", "DOW-CRC", "1-WIRE"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AlgorithmLabels() = %v, want %v", got, want)
	}
	if name := def.AlgorithmName(); name != "CRC-8/MAXIM-DOW" {
		t.Errorf("AlgorithmName() = %q, want %q", name, "CRC-8/MAXIM-DOW")
	}
}

// TestAlgorithmLabelsPresetNoAliases covers a preset with no catalogued
// aliases (e.g. plain "CRC-8") — AlgorithmLabels must still return exactly
// one candidate, not nil, so callers don't have to special-case "no
// aliases" as "no name at all".
func TestAlgorithmLabelsPresetNoAliases(t *testing.T) {
	def := Definition{Mode: ModePreset, Preset: "CRC-8"}
	got := def.AlgorithmLabels()
	want := []string{"CRC-8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AlgorithmLabels() = %v, want %v", got, want)
	}
}

// TestAlgorithmLabelsUnknownPreset covers a Preset name that doesn't
// resolve (e.g. a protocol profile saved against a since-removed preset) —
// the raw stored name is still surfaced rather than silently disappearing.
func TestAlgorithmLabelsUnknownPreset(t *testing.T) {
	def := Definition{Mode: ModePreset, Preset: "CRC-NOT-A-REAL-PRESET"}
	got := def.AlgorithmLabels()
	want := []string{"CRC-NOT-A-REAL-PRESET"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AlgorithmLabels() = %v, want %v", got, want)
	}
	if name := def.AlgorithmName(); name != "CRC-NOT-A-REAL-PRESET" {
		t.Errorf("AlgorithmName() = %q, want the raw preset name", name)
	}
}

// TestAlgorithmLabelsCustom covers a user-authored (non-catalogued) CRC —
// there's no real name to surface, so AlgorithmLabels synthesizes a
// descending ladder ending in the bare "CRC" every width can fall back to.
func TestAlgorithmLabelsCustom(t *testing.T) {
	def := Definition{Mode: ModeCustom, Custom: Params{Width: 16}}
	got := def.AlgorithmLabels()
	want := []string{"CUSTOM CRC-16", "CRC-16", "CRC"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AlgorithmLabels() = %v, want %v", got, want)
	}
	if name := def.AlgorithmName(); name != "CUSTOM CRC-16" {
		t.Errorf("AlgorithmName() = %q, want %q", name, "CUSTOM CRC-16")
	}
}

// TestAlgorithmLabelsNone covers a disabled/unset checksum — nil labels,
// but AlgorithmName still returns a printable fallback rather than "".
func TestAlgorithmLabelsNone(t *testing.T) {
	for _, def := range []Definition{{}, {Mode: ModeNone}} {
		if got := def.AlgorithmLabels(); got != nil {
			t.Errorf("AlgorithmLabels() = %v, want nil for mode %q", got, def.Mode)
		}
		if name := def.AlgorithmName(); name != "CRC" {
			t.Errorf("AlgorithmName() = %q, want fallback %q", name, "CRC")
		}
	}
}
