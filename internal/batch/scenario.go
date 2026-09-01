// Package batch implements scripted hardware tests: send a packet, wait
// for a reply, decode it against the same packet.Schema used everywhere
// else, assert on fields and CRC validity, PASS/FAIL. It builds directly
// on internal/session and internal/packet — it does not reimplement
// serialization, decoding, or the CRC engine (see ARCHITECTURE.md's "batch
// must not duplicate the packet serializer" invariant).
package batch

import (
	"time"
)

// Scenario is one batch test file: which protocol/device it expects (purely
// informational at this layer — cmd/serialforge resolves Protocol to a
// packet.Schema and Device to an open session.Session before calling Run;
// this package never touches config/device/protocol stores itself) and an
// ordered list of steps.
type Scenario struct {
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
	Protocol string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	Device   string `yaml:"device,omitempty" json:"device,omitempty"`
	Steps    []Step `yaml:"steps" json:"steps"`
}

// Step is a tagged union: exactly one field should be set, mirroring the
// product spec's illustrative "- send_packet: {...}" YAML shape. Unlike a
// Go type-switch interface, this keeps the scenario file plain, readable
// YAML with no custom unmarshaler required.
type Step struct {
	Send         *SendStep         `yaml:"send,omitempty" json:"send,omitempty"`
	SendPacket   *SendPacketStep   `yaml:"send_packet,omitempty" json:"send_packet,omitempty"`
	Sleep        *SleepStep        `yaml:"sleep,omitempty" json:"sleep,omitempty"`
	Expect       *ExpectStep       `yaml:"expect,omitempty" json:"expect,omitempty"`
	ExpectHex    *ExpectHexStep    `yaml:"expect_hex,omitempty" json:"expect_hex,omitempty"`
	ExpectPacket *ExpectPacketStep `yaml:"expect_packet,omitempty" json:"expect_packet,omitempty"`
	AssertField  *AssertFieldStep  `yaml:"assert_field,omitempty" json:"assert_field,omitempty"`
	AssertCRC    *AssertCRCStep    `yaml:"assert_crc,omitempty" json:"assert_crc,omitempty"`
	ClearRX      *ClearRXStep      `yaml:"clear_rx,omitempty" json:"clear_rx,omitempty"`
	Log          *LogStep          `yaml:"log,omitempty" json:"log,omitempty"`
}

// SendStep writes raw bytes, given as a hex string ("AA 55 02" or "aa5502").
type SendStep struct {
	Hex string `yaml:"hex" json:"hex"`
}

// SendPacketStep serializes Fields against the scenario's schema (CRC
// AUTO-computed unless CRCOverride is set — the same fault-injection
// override packet.Serialize exposes everywhere else) and sends the result.
// Field values are hex strings, e.g. {"command": "02", "address": "00C017FF"}.
type SendPacketStep struct {
	Fields      map[string]string `yaml:"fields" json:"fields"`
	CRCOverride *string           `yaml:"crc_override,omitempty" json:"crc_override,omitempty"` // hex
}

// SleepStep pauses for Duration (Go duration syntax: "500ms", "2s").
type SleepStep struct {
	Duration string `yaml:"duration" json:"duration"`
}

// ExpectStep waits up to Timeout for the next RX frame and compares it
// byte-for-byte against Hex.
type ExpectStep struct {
	Hex     string `yaml:"hex" json:"hex"`
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"` // default 1s
}

// ExpectHexStep is an alias of ExpectStep kept as a distinct step name to
// match the product spec's step list verbatim; behaves identically.
type ExpectHexStep = ExpectStep

// ExpectPacketStep waits up to Timeout for the next RX frame and decodes it
// against the scenario's schema, storing the result for subsequent
// assert_field/assert_crc steps.
type ExpectPacketStep struct {
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"` // default 1s
}

// AssertFieldStep checks the most recently expect_packet-decoded packet's
// named field against Equals (a hex string, compared against the field's
// raw bytes — works for any Format since Raw is always authoritative).
type AssertFieldStep struct {
	Field  string `yaml:"field" json:"field"`
	Equals string `yaml:"equals" json:"equals"`
}

// AssertCRCStep checks the most recently expect_packet-decoded packet's
// CRC validity against Valid.
type AssertCRCStep struct {
	Valid bool `yaml:"valid" json:"valid"`
}

// ClearRXStep discards any already-queued-but-unconsumed RX events, so a
// subsequent expect/expect_packet only sees frames that arrive after this
// point.
type ClearRXStep struct{}

// LogStep emits Message into the report without affecting pass/fail.
type LogStep struct {
	Message string `yaml:"message" json:"message"`
}

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}
