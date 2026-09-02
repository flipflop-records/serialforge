// Package savedpacket persists reusable, named packets: a reference to a
// Protocol Profile, concrete field values, and a CRC mode — so a recurring
// command (GET_STATUS, RESET, PING, ...) can be built once and sent
// instantly instead of being rebuilt field-by-field every time. It is pure
// model + persistence, the same shape as internal/protocol: SavedPacket
// never embeds a copy of the protocol's schema, never reimplements
// serialization, and never caches a computed CRC byte as if it were the
// source of truth. internal/tui's Saved Packets screen, TX Builder's
// save/load/update actions, and cmd/serialforge's `saved` command group all
// build packets through SavedPacket.Build — the one call site that ever
// turns a SavedPacket into wire bytes.
package savedpacket

import (
	"fmt"
	"strings"

	"github.com/vtemnyakov/serialforge/internal/packet"
	"github.com/vtemnyakov/serialforge/internal/protocol"
)

// CRCMode selects whether a SavedPacket's CRC is recalculated fresh on every
// build (Auto — the normal case) or transmitted exactly as stored
// (Override — intentional fault injection, e.g. for negative testing a
// device's CRC checking). Auto is not "compute once and cache": Build always
// resolves the current schema and recomputes from current values, so a
// later protocol/field edit is picked up automatically — see Build's doc
// comment and ARCHITECTURE.md's CRC presentation invariants.
type CRCMode string

const (
	CRCModeAuto     CRCMode = "auto"
	CRCModeOverride CRCMode = "override"
)

// SavedPacket is one persisted, reusable packet: which Protocol Profile it
// belongs to (a reference by name, resolved fresh on every use — never an
// embedded schema copy, so the protocol remains the single source of truth
// for field order/size/endianness/CRC algorithm/packet size/serialization,
// per the product requirement this package exists to satisfy), the field
// values to serialize, the CRC mode, and an optional single-key hotkey.
//
// Values is keyed by field name. This is only sound because
// packet.Schema.Validate rejects duplicate field names
// (internal/packet/schema.go, TestValidateRejectsDuplicateFieldNames) — any
// schema Serialize/Build/Decode will actually accept has unique field names
// by construction, so a name-keyed map can never silently collapse two
// fields. The one gap is that protocol.Store.Put deliberately accepts
// invalid/incomplete draft schemas (the Designer needs to persist
// in-progress work) — see protocol.Store's doc comment — so a *stored*
// protocol can transiently fail Validate (duplicate names included).
// Resolve accounts for this explicitly: it checks schema.IsValid() before
// doing any field-name-keyed lookup and reports StatusProtocolInvalid
// rather than assuming a loaded schema is safe to index by name.
type SavedPacket struct {
	Name        string            `yaml:"name" json:"name"`
	Protocol    string            `yaml:"protocol" json:"protocol"`
	Values      map[string]string `yaml:"values" json:"values"` // field name -> hex string, same representation TX Builder edits
	CRCMode     CRCMode           `yaml:"crc_mode" json:"crc_mode"`
	CRCOverride string            `yaml:"crc_override,omitempty" json:"crc_override,omitempty"` // hex string; CRCModeOverride only
	Hotkey      string            `yaml:"hotkey,omitempty" json:"hotkey,omitempty"`             // stable single-key string, e.g. "'"; "" = unbound
}

// Status is the outcome of resolving a SavedPacket against the current
// protocol store — never a hard error on its own; a broken/stale SavedPacket
// is a diagnosable, displayable state (product requirement: "do not crash,
// do not silently send malformed packets").
type Status string

const (
	StatusOK              Status = "ok"
	StatusProtocolMissing Status = "protocol_missing" // the referenced protocol no longer exists
	StatusProtocolInvalid Status = "protocol_invalid" // the referenced protocol exists but fails Schema.Validate (e.g. a stale draft)
	StatusIncompatible    Status = "incompatible"     // protocol is valid, but stored values no longer match its schema
)

// ProblemKind classifies one field-level incompatibility between a
// SavedPacket's stored values and the current schema.
type ProblemKind string

const (
	ProblemMissingValue ProblemKind = "missing_value" // schema has this field; SavedPacket has no stored value for it (field added since save)
	ProblemUnknownField ProblemKind = "unknown_field" // SavedPacket has a stored value for a field the schema no longer defines (field deleted)
	ProblemSizeMismatch ProblemKind = "size_mismatch" // stored hex value's byte length no longer matches the field's current size
)

// FieldProblem is one concrete reason a SavedPacket can't be built as-is.
type FieldProblem struct {
	Field string
	Kind  ProblemKind
}

// String is a short, user-facing description, e.g. "missing field: flags".
func (p FieldProblem) String() string {
	switch p.Kind {
	case ProblemMissingValue:
		return "missing field: " + p.Field
	case ProblemUnknownField:
		return "stale field (no longer in protocol): " + p.Field
	case ProblemSizeMismatch:
		return "value no longer fits: " + p.Field
	default:
		return p.Field
	}
}

// Resolution is the result of resolving a SavedPacket against a protocol
// store: its status, the live schema (valid only when Status == StatusOK or
// StatusIncompatible — StatusIncompatible still carries the schema so a
// caller can render the diagram/problems against it), and every field-level
// problem found.
type Resolution struct {
	Status   Status
	Schema   packet.Schema
	Problems []FieldProblem
	err      error // set for StatusProtocolInvalid — the Validate() error
}

// Err returns the underlying schema-validation error for StatusProtocolInvalid, nil otherwise.
func (r Resolution) Err() error { return r.err }

// Resolve looks up sp.Protocol in protocols and compares sp.Values against
// its current schema. It always re-fetches the schema — never a cached copy
// — so a protocol edited after this SavedPacket was created is picked up
// automatically (the "protocol remains source of truth" requirement).
func (sp SavedPacket) Resolve(protocols *protocol.Store) Resolution {
	sc, ok := protocols.Get(sp.Protocol)
	if !ok {
		return Resolution{Status: StatusProtocolMissing}
	}
	if err := sc.Validate(); err != nil {
		return Resolution{Status: StatusProtocolInvalid, Schema: sc, err: err}
	}

	var problems []FieldProblem
	seen := make(map[string]bool, len(sc.Fields))
	for _, f := range sc.Fields {
		seen[f.Name] = true
		hexVal, ok := sp.Values[f.Name]
		if !ok {
			problems = append(problems, FieldProblem{Field: f.Name, Kind: ProblemMissingValue})
			continue
		}
		clean := cleanHex(hexVal)
		if len(clean)%2 != 0 || len(clean)/2 != f.Size {
			problems = append(problems, FieldProblem{Field: f.Name, Kind: ProblemSizeMismatch})
		}
	}
	for name := range sp.Values {
		if !seen[name] {
			problems = append(problems, FieldProblem{Field: name, Kind: ProblemUnknownField})
		}
	}

	status := StatusOK
	if len(problems) > 0 {
		status = StatusIncompatible
	}
	return Resolution{Status: status, Schema: sc, Problems: problems}
}

// Build resolves sp against protocols and, if compatible, serializes it
// through packet.Build — the exact function TX Builder and the CLI's
// `packet build` use, so a Saved Packet produces byte-identical output to
// building the same values by hand. CRC handling follows CRCMode: Auto
// passes a nil override, so packet.Build/Serialize always recomputes from
// the current schema/values (never a stored, stale CRC byte); Override
// parses CRCOverride and passes it through, preserved exactly as an
// intentional fault-injection value.
func (sp SavedPacket) Build(protocols *protocol.Store) (*packet.Packet, error) {
	res := sp.Resolve(protocols)
	switch res.Status {
	case StatusProtocolMissing:
		return nil, fmt.Errorf("saved packet %q: protocol %q not found", sp.Name, sp.Protocol)
	case StatusProtocolInvalid:
		return nil, fmt.Errorf("saved packet %q: protocol %q is not a valid schema: %w", sp.Name, sp.Protocol, res.err)
	case StatusIncompatible:
		return nil, fmt.Errorf("saved packet %q: incompatible with protocol %q (%s)", sp.Name, sp.Protocol, res.Problems[0].String())
	}

	values := packet.Values{}
	for _, f := range res.Schema.Fields {
		raw, err := decodeHex(sp.Values[f.Name])
		if err != nil {
			return nil, fmt.Errorf("saved packet %q: field %q: %w", sp.Name, f.Name, err)
		}
		values[f.Name] = raw
	}

	var crcOverride *uint64
	if sp.CRCMode == CRCModeOverride {
		v, err := parseHexUint(sp.CRCOverride)
		if err != nil {
			return nil, fmt.Errorf("saved packet %q: CRC override: %w", sp.Name, err)
		}
		crcOverride = &v
	}

	return packet.Build(res.Schema, values, crcOverride)
}

// --- small hex helpers -------------------------------------------------
//
// Mirrors the small, self-contained hex-cleanup helpers already duplicated
// per package in this codebase (cmd/serialforge's cleanHex,
// internal/tui's cleanHexTUI) rather than introducing a shared package for
// one helper — consistent with how the rest of the codebase already
// handles this.

func cleanHex(s string) string {
	return strings.NewReplacer(" ", "", "0x", "", "0X", "", "\t", "", "\n", "").Replace(s)
}

func decodeHex(s string) ([]byte, error) {
	clean := cleanHex(s)
	if len(clean)%2 != 0 {
		return nil, fmt.Errorf("odd number of hex digits in %q", s)
	}
	out := make([]byte, len(clean)/2)
	for i := range out {
		var b int
		if _, err := fmt.Sscanf(clean[i*2:i*2+2], "%02X", &b); err != nil {
			return nil, fmt.Errorf("invalid hex %q: %w", s, err)
		}
		out[i] = byte(b)
	}
	return out, nil
}

func parseHexUint(s string) (uint64, error) {
	raw, err := decodeHex(s)
	if err != nil {
		return 0, err
	}
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty hex value")
	}
	var v uint64
	for _, b := range raw {
		v = v<<8 | uint64(b)
	}
	return v, nil
}
